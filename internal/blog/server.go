package blog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type Server struct {
	bm           *BlogManager
	tracer       trace.Tracer
	srv          *http.Server
	lts          *LocalTelemetryStorage
	startTime    time.Time
	articleViews metric.Int64Counter
	badReq       metric.Int64Counter
	roboVisit    metric.Int64Counter
	errChan      chan error
	sigChan      chan os.Signal
}

func NewServer(bm *BlogManager, ls *LocalTelemetryStorage) *Server {
	meter := otel.GetMeterProvider().Meter("jake-blog")

	articleViews, err := meter.Int64Counter(
		"articles.served",
		metric.WithDescription("Number of times a blog article has been requested"),
	)
	if err != nil {
		return nil
	}

	badRequest, err := meter.Int64Counter(
		"request.blocked", metric.WithDescription("number of requests blocked by middleware"),
	)
	if err != nil {
		return nil
	}

	robo, err := meter.Int64Counter(
		"robotic.visitors", metric.WithDescription("number of times someone has requested robots.txt"),
	)
	if err != nil {
		return nil
	}

	return &Server{
		bm:           bm,
		tracer:       otel.Tracer("jake-blog"),
		startTime:    time.Now(),
		articleViews: articleViews,
		badReq:       badRequest,
		roboVisit:    robo,
		errChan:      make(chan error, 1),
		sigChan:      make(chan os.Signal, 1),
		lts:          ls,
	}
}

func (s *Server) Start(ctx context.Context) error {
	s.srv = &http.Server{
		Handler:      s.SetupRoutes(),
		Addr:         ":" + s.bm.Config.ServerPort,
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}

	serverLogger.Info().Msgf("https enabled: %t", s.bm.Config.HTTPSOn)

	signal.Notify(s.sigChan, syscall.SIGINT, syscall.SIGTERM)

	serverLogger.Info().Msgf("server bound to port %s", s.bm.Config.ServerPort)

	if s.bm.Config.HTTPSOn {
		go func() {
			err := s.srv.ListenAndServeTLS(s.bm.Config.HTTPSCRT, s.bm.Config.HTTPSKey)
			if err != nil {
				s.errChan <- fmt.Errorf("server error: %w", err)
			}
		}()
		// open http server to redirect to https
		go func() {
			redirectSrv := &http.Server{
				Addr:         ":80",
				Handler:      http.HandlerFunc(s.RedirectHandler),
				WriteTimeout: 15 * time.Second,
				ReadTimeout:  15 * time.Second,
			}

			err := redirectSrv.ListenAndServe()
			if err != nil {
				s.errChan <- fmt.Errorf("redirect server error: %w", err)
			}
		}()

	} else {
		go func() {
			err := s.srv.ListenAndServe()
			if err != nil {
				s.errChan <- fmt.Errorf("server error: %w", err)
			}
		}()
	}

	// Wait for shutdown signal
	select {
	case <-ctx.Done():
		serverLogger.Warn().Msg("context cancelled")
	case sig := <-s.sigChan:
		serverLogger.Warn().Msgf("%s received", sig)
	case err := <-s.errChan:
		return fmt.Errorf("server error before shutdown: %w", err)
	}

	return s.shutdown()
}

func (s *Server) shutdown() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Attempt graceful shutdown
	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown error: %w", err)
	}

	serverLogger.Info().Msg("shutdown complete")
	return nil
}

func (s *Server) SetupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.Handle("/", s.wrapHandler(
		http.FileServer(http.Dir("/web")),
		"static file server",
	))

	mux.Handle("/article/images/", s.wrapHandler(
		http.StripPrefix("/article/images/",
			http.FileServer(http.Dir(s.bm.Config.ContentDir+"/images"))),
		"image file server",
	))

	// Content handlers
	mux.Handle("/content/", s.wrapHandler(
		http.HandlerFunc(s.ArticleList),
		"article list",
	))
	mux.Handle("/article/", s.wrapHandler(
		http.HandlerFunc(s.Article),
		"article handler",
	))

	mux.Handle("/feed/", s.wrapHandler(
		http.HandlerFunc(s.RssFeedHandler),
		"RSS Feed Handler",
	))

	mux.Handle("/robots.txt", s.wrapHandler(
		http.HandlerFunc(s.RobotsHandler),
		"robots.txt handler",
	))

	mux.Handle("/sitemap.xml", s.wrapHandler(
		http.HandlerFunc(s.SiteMapHandler),
		"sitemap handler",
	))

	mux.HandleFunc("/telemetry/trace", s.LastTrace)
	mux.HandleFunc("/telemetry/metric", s.MetricSnippet)
	mux.HandleFunc("/telemetry/cost", s.CostSnippet)

	return mux
}

func (s *Server) reqBlockedInstrument(reason string, ctx context.Context) {
	s.badReq.Add(
		ctx,
		1,
		metric.WithAttributes(attribute.String("blocked", reason)),
	)
	s.badReq.Add(
		ctx,
		1,
	)
}

func (s *Server) wrapHandler(h http.Handler, name string) http.Handler {
	validateHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) > 1024 {
			s.reqBlockedInstrument("URI_LENGTH", r.Context())
			http.Error(w, "URI too long", http.StatusBadRequest)
			return
		}

		if r.Method != http.MethodGet {
			s.reqBlockedInstrument("BAD_METHOD", r.Context())
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if strings.ContainsRune(r.URL.Path, 0xfffd) { // inavlid utf-8 characters
			s.reqBlockedInstrument("INVALID_CHAR_URL", r.Context())
			http.Error(w, "Invalid URL characters", http.StatusBadRequest)
			return
		}

		if strings.Contains(r.URL.Path, "%00") || strings.Contains(r.URL.Path, "\x00") { // null termination
			s.reqBlockedInstrument("INVALID_CHAR_URL", r.Context())
			http.Error(w, "Invalid URL characters", http.StatusBadRequest)
			return
		}

		// add csp headers
		w.Header().Set("Content-Security-Policy", `default-src 'self'; script-src 'self'; script-src-elem 'self'; style-src 'self' ; img-src 'self' https://jakeblog-blog-image-cache.s3.us-east-1.amazonaws.com; connect-src 'self'`)

		h.ServeHTTP(w, r)
	})

	return otelhttp.NewHandler(validateHandler, name,
		otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
			return "Serve " + r.URL.Path
		}),
		otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents),
	)
}

func (s *Server) ArticleList(w http.ResponseWriter, r *http.Request) {
	_, span := s.tracer.Start(r.Context(), "ArticleListHandler.Process")
	defer span.End()

	s.bm.articleMutex.RLock()
	defer s.bm.articleMutex.RUnlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write(s.bm.HTMLList)
	if err != nil {
		span.SetAttributes(attribute.String("error", "failed to write articlelist"))
	}
}

func (s *Server) RedirectHandler(w http.ResponseWriter, r *http.Request) {
	target := "https://" + r.Host + r.URL.Path

	if len(r.URL.RawQuery) > 0 {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusPermanentRedirect)
}

func (s *Server) Article(w http.ResponseWriter, r *http.Request) {
	_, span := s.tracer.Start(r.Context(), "ArticleHandler.Process")
	defer span.End()

	unescaped, err := url.QueryUnescape(r.URL.Path)
	if err != nil {
		span.SetAttributes(attribute.String("error", "invalid url encoding"))
		http.Error(w, "invalid url encoding", http.StatusBadRequest)
		return
	}

	articleName := path.Base(unescaped)
	span.SetAttributes(attribute.String("article.name", articleName))

	article, exists := s.bm.GetArticle(articleName)
	if !exists {
		span.SetAttributes(attribute.String("error", "article not found"))
		http.NotFound(w, r)
		return
	}

	s.articleViews.Add(
		r.Context(),
		1,
		metric.WithAttributes(attribute.String("article", articleName)),
	)
	s.articleViews.Add(
		r.Context(),
		1,
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(article.Content) // #nosec G705 -- content is from our own git repo, not user input
	if err != nil {
		serverLogger.Error().Msgf("failed to send article to client: %v", err)
		span.SetAttributes(attribute.String("error", "write failed"))
	}
}

func (s *Server) LastTrace(w http.ResponseWriter, r *http.Request) {
	jsonStr, err := s.lts.GetLastSpanJSON()
	if err != nil {
		http.Error(w, `{"error": "Failed to get trace data"}`, http.StatusInternalServerError)
		return
	}

	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, []byte(jsonStr), "", "  "); err != nil {
		http.Error(w, `{"error": "Failed to format JSON"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(prettyJSON.Bytes())
	if err != nil {
		serverLogger.Error().Msgf("failed to send trace data: %v", err)
	}
}

// errWriter wraps an io.Writer and tracks the first write error.
// Subsequent writes are skipped once an error occurs.
type errWriter struct {
	w    io.Writer
	itoa [20]byte
	err  error
}

func (ew *errWriter) str(s string) {
	if ew.err != nil {
		return
	}
	_, ew.err = io.WriteString(ew.w, s)
}

func (ew *errWriter) int64(v int64) {
	if ew.err != nil {
		return
	}
	_, ew.err = ew.w.Write(strconv.AppendInt(ew.itoa[:0], v, 10))
}

// writeMetricSnippet writes metrics HTML directly to w, avoiding intermediate allocations.
func (s *Server) writeMetricSnippet(w io.Writer) error {
	ew := errWriter{w: w}

	ew.str("<p>blog.uptime: ")
	ew.str(time.Since(s.startTime).String())
	ew.str("</p>")

	ew.str("<p>blog.articles.served: ")
	ew.int64(s.lts.articlesServed.Load())
	ew.str("</p>")
	orderedKeys := make([]string, 0, len(s.lts.servedCountPerArticle))
	for art := range s.lts.servedCountPerArticle {
		orderedKeys = append(orderedKeys, art)
	}
	sort.Strings(orderedKeys)
	for _, aname := range orderedKeys {
		counter, exists := s.lts.servedCountPerArticle[aname]
		if exists {
			ew.str("<p>blog.articles.served.")
			ew.str(aname)
			ew.str(": ")
			ew.int64(counter.Load())
			ew.str("</p>")
		} else {
			serverLogger.Warn().Msgf("could not load article freq metrics article does not exist in map %s", aname)
		}
	}

	ew.str("<p>blog.requests.blocked: ")
	ew.int64(s.lts.reqBlocked.Load())
	ew.str("</p>")
	orderedReasons := make([]string, 0, len(s.lts.reqBlockedByReason))
	for reason := range s.lts.reqBlockedByReason {
		orderedReasons = append(orderedReasons, reason)
	}
	sort.Strings(orderedReasons)
	for _, res := range orderedReasons {
		counter, exists := s.lts.reqBlockedByReason[res]
		if exists {
			ew.str("<p>blog.requests.blocked.")
			ew.str(res)
			ew.str(": ")
			ew.int64(counter.Load())
			ew.str("</p>")
		}
	}

	ew.str("<p>blog.requests.robots: ")
	ew.int64(s.lts.roboticVisitors.Load())
	ew.str("</p>")

	ew.str("<p>blog.server.request.ms.p50: ")
	ew.int64(s.lts.reqDur50.Load())
	ew.str("</p>")
	ew.str("<p>blog.server.request.ms.p90: ")
	ew.int64(s.lts.reqDur90.Load())
	ew.str("</p>")
	ew.str("<p>blog.server.request.ms.p95: ")
	ew.int64(s.lts.reqDur95.Load())
	ew.str("</p>")
	ew.str("<p>blog.server.request.ms.p99: ")
	ew.int64(s.lts.reqDur99.Load())
	ew.str("</p>")

	ew.str("<p>blog.goroutine.count: ")
	ew.int64(s.lts.numGoRo.Load())
	ew.str("</p>")

	ew.str("<p>blog.heap.alloc.bytes: ")
	ew.int64(s.lts.heapAlloc.Load())
	ew.str("</p>")

	ew.str("<p>blog.stack.alloc.bytes: ")
	ew.int64(s.lts.stackAlloc.Load())
	ew.str("</p>")

	ew.str("<p>blog.cost.update.success: ")
	ew.int64(s.lts.costUpdateSuccess.Load())
	ew.str("</p>")

	ew.str("<p>blog.cost.update.failure: ")
	ew.int64(s.lts.costUpdateFailure.Load())
	ew.str("</p>")

	if ew.err == nil {
		ew.str("<p>Last Updated: ")
		_, ew.err = ew.w.Write(time.Now().AppendFormat(ew.itoa[:0], "2006-01-02 15:04:05"))
		ew.str("</p>")
	}

	return ew.err
}

func (s *Server) MetricSnippet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.writeMetricSnippet(w); err != nil {
		serverLogger.Error().Msgf("failed to write metric snippet: %v", err)
	}
}

func (s *Server) CostSnippet(w http.ResponseWriter, r *http.Request) {
	s.lts.costMu.RLock()
	costHTML := s.lts.costHTML
	s.lts.costMu.RUnlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write(costHTML)
	if err != nil {
		serverLogger.Error().Msgf("failed to send cost data: %v", err)
	}
}

func (s *Server) RssFeedHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/rss+xml")
	feedContent := s.bm.GetRssFeed()
	_, err := w.Write(feedContent)
	if err != nil {
		serverLogger.Error().Msgf("failed tp send rss feed to client: %v", err)
	}
}

func (s *Server) SiteMapHandler(w http.ResponseWriter, r *http.Request) {
	smap := s.bm.GetSiteMap()
	_, err := w.Write(smap)
	if err != nil {
		serverLogger.Error().Msgf("failed to send sitemap to client: %v", err)
	}
}

func (s *Server) RobotsHandler(w http.ResponseWriter, r *http.Request) {
	smap := "User-agent: *\n" +
		"Disallow: /content\n" +
		"Disallow: /telemetry/\n\n" +
		"Sitemap: https://jake-henning.com/sitemap.xml"
	_, err := w.Write([]byte(smap))
	if err != nil {
		serverLogger.Error().Msgf("failed to write robots.txt: %v", err)
	}
	s.roboVisit.Add(
		r.Context(),
		1,
	)
}
