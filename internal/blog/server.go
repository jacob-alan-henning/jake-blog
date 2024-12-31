package blog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
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

	signal.Notify(s.sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Starting server on port %s", s.bm.Config.ServerPort)
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.errChan <- fmt.Errorf("server error: %w", err)
		}
	}()

	// Wait for shutdown signal
	select {
	case <-ctx.Done():
		log.Println("Context cancelled, shutting down server...")
	case sig := <-s.sigChan:
		log.Printf("Received signal %s, shutting down server...", sig)
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

	log.Println("Server shutdown complete")
	return nil
}

func (s *Server) SetupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// Static file servers with OTEL wrapping
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
	mux.HandleFunc("/content/", s.ArticleList)
	mux.HandleFunc("/article/", s.Article)
	mux.HandleFunc("/telemetry/trace", s.LastTrace)
	mux.HandleFunc("/telemetry/metric", s.MetricSnippet)

	return mux
}

func (s *Server) wrapHandler(h http.Handler, name string) http.Handler {
	return otelhttp.NewHandler(h, name,
		otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
			return fmt.Sprintf("Serve %s", r.URL.Path)
		}),
		otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents),
	)
}

func (s *Server) ArticleList(w http.ResponseWriter, r *http.Request) {
	_, span := s.tracer.Start(r.Context(), "ArticleListHandler.Process")
	defer span.End()

	if r.Method != http.MethodGet {
		span.SetAttributes(attribute.String("error", "method not allowed"))
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.bm.articleMutex.RLock()
	defer s.bm.articleMutex.RUnlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, s.bm.HtmlList)
}

func (s *Server) Article(w http.ResponseWriter, r *http.Request) {
	_, span := s.tracer.Start(r.Context(), "ArticleHandler.Process")
	defer span.End()

	if r.Method != http.MethodGet {
		span.SetAttributes(attribute.String("error", "method not allowed"))
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	articleName := path.Base(r.URL.Path)
	span.SetAttributes(attribute.String("article.name", articleName))

	article, exists := s.bm.GetArticle(articleName)
	if !exists {
		span.SetAttributes(attribute.String("error", "article not found"))
		http.NotFound(w, r)
		return
	}

	s.articleViews.Add(r.Context(), 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, article.Content)
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
	w.Write(prettyJSON.Bytes())
}

func (s *Server) MetricSnippet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	uptime := time.Since(s.startTime)
	count := s.lts.GetArticlesServed()
	fmt.Fprintf(w, "<p>Uptime: %s</p><p>Articles Served: %d</p>", uptime, count)
}
