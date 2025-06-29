package blog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
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
	errChan      chan error
	sigChan      chan os.Signal
}

// make this return an err properly!!!!!
func NewServer(bm *BlogManager, ls *LocalTelemetryStorage) *Server {
	meter := otel.GetMeterProvider().Meter("jake-blog")

	articleViews, err := meter.Int64Counter(
		"articles.served",
		metric.WithDescription("Number of times a blog article has been requested"),
	)
	if err != nil {
		return nil
	}

	return &Server{
		bm:           bm,
		tracer:       otel.Tracer("jake-blog"),
		startTime:    time.Now(),
		articleViews: articleViews,
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

	log.Printf("https enabled: %t", s.bm.Config.HTTPSOn)

	signal.Notify(s.sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("starting server on port %s", s.bm.Config.ServerPort)

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
		log.Println("context cancelled, shutting down server...")
	case sig := <-s.sigChan:
		log.Printf("received signal %s, shutting down server...", sig)
	case err := <-s.errChan:
		return fmt.Errorf("server error before shutdown: %w", err)
	}

	return s.shutdown()
}

func (s *Server) shutdown() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Attempt graceful shutdown
	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown error: %w", err)
	}

	log.Println("server shutdown complete")
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

	mux.HandleFunc("/telemetry/trace", s.LastTrace)
	mux.HandleFunc("/telemetry/metric", s.MetricSnippet)

	return mux
}

func (s *Server) wrapHandler(h http.Handler, name string) http.Handler {
	validateHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) > 1024 {
			http.Error(w, "URI too long", http.StatusBadRequest)
			return
		}

		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if strings.ContainsRune(r.URL.Path, 0xfffd) { // inavlid utf-8 characters
			http.Error(w, "Invalid URL characters", http.StatusBadRequest)
			return
		}

		if strings.Contains(r.URL.Path, "%00") || strings.Contains(r.URL.Path, "\x00") { // null termination
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
	_, err := w.Write(s.bm.HtmlList)
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

	s.articleViews.Add(r.Context(), 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = w.Write(article.Content)
	if err != nil {
		log.Printf("failed to write article response %v", err)
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
		log.Print("failed to write trace data")
	}
}

func (s *Server) MetricSnippet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	var metricBuilder strings.Builder

	uptime := time.Since(s.startTime)
	metricBuilder.WriteString(fmt.Sprintf("<p>blog.process.uptime: %s</p>", uptime))

	metricBuilder.WriteString(fmt.Sprintf("<p>blog.articles.served: %d</p>", s.lts.articlesServed.Load()))

	metricBuilder.WriteString(fmt.Sprintf("<p>blog.server.request.ms.p50: %d</p>", s.lts.reqDur50.Load()))
	metricBuilder.WriteString(fmt.Sprintf("<p>blog.server.request.ms.p90: %d</p>", s.lts.reqDur90.Load()))
	metricBuilder.WriteString(fmt.Sprintf("<p>blog.server.request.ms.p95: %d</p>", s.lts.reqDur95.Load()))
	metricBuilder.WriteString(fmt.Sprintf("<p>blog.server.request.ms.p99: %d</p>", s.lts.reqDur99.Load()))

	metricBuilder.WriteString(fmt.Sprintf("<p>blog.goroutine.count: %d</p>", s.lts.numGoRo.Load()))

	metricBuilder.WriteString(fmt.Sprintf("<p>blog.heap.alloc.bytes: %d</p>", s.lts.heapAlloc.Load()))

	metricBuilder.WriteString(fmt.Sprintf("<p>blog.stack.alloc.bytes: %d</p>", s.lts.stackAlloc.Load()))

	buf := make([]byte, 0, 19)
	buf = time.Now().AppendFormat(buf, "2006-01-02 15:04:05")
	dtStr := string(buf)
	metricBuilder.WriteString(fmt.Sprintf("<p>Last Updated: %s</p>", dtStr))

	fmt.Fprint(w, metricBuilder.String())
}

func (s *Server) RssFeedHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/rss+xml")
	feedContent := s.bm.GetRssFeed()
	_, err := w.Write(feedContent)
	if err != nil {
		log.Printf("failed to write rss feed %v", err)
	}
}
