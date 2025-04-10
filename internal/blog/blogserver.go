package blog

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type BlogServer struct {
	server *Server                // http server
	bm     *BlogManager           // content management
	telem  *LocalTelemetryStorage // where otel data is exported
	cfg    Config                 // configuration settings

	// Control channels
	ctx     context.Context
	cancel  context.CancelFunc
	sigChan chan os.Signal
}

type BlogServerOption func(*BlogServer) error

func WithConfig(envPrefix string) BlogServerOption {
	return func(bs *BlogServer) error {
		cfg, err := NewConfig(WithEnvironment(envPrefix))
		if err != nil {
			return fmt.Errorf("config creation failed: %w", err)
		}
		bs.cfg = *cfg

		if err := bs.cfg.InitializePrivateKey(); err != nil { // side effects
			return fmt.Errorf("private key initialization failed: %w", err)
		}
		return nil
	}
}

func NewBlogServer(opts ...BlogServerOption) (*BlogServer, error) {
	ctx, cancel := context.WithCancel(context.Background())
	lts := NewLocalTelemetryStorage()

	bs := &BlogServer{
		ctx:     ctx,
		cancel:  cancel,
		sigChan: make(chan os.Signal, 1),
		telem:   lts,
	}

	// Apply all options
	for _, opt := range opts {
		if err := opt(bs); err != nil {
			cancel() // Clean up if any option fails
			return nil, err
		}
	}

	bs.bm = NewBlogManager(&bs.cfg)
	bs.server = NewServer(bs.bm, bs.telem)
	if bs.server == nil {
		return nil, fmt.Errorf("could not initialize server")
	}

	return bs, nil
}

func (bs *BlogServer) Start() error {
	signal.Notify(bs.sigChan, syscall.SIGINT, syscall.SIGTERM)
	// start localtemetrystorage
	err := bs.telem.Start(bs.ctx)
	if err != nil {
		return fmt.Errorf("failed to start telemetry: %w", err)
	}
	err = bs.InstallExportPipeline(bs.ctx)
	if err != nil {
		return fmt.Errorf("failed to install export pipeline: %w", err)
	}

	// Start blog manager updates
	bs.bm.ListenForUpdates(bs.ctx)
	bs.bm.TriggerUpdate()

	err = bs.server.Start(bs.ctx)
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	return bs.run()
}

func (bs *BlogServer) run() error {
	sig := <-bs.sigChan
	log.Printf("Received signal %s, initiating shutdown...", sig)
	return bs.shutdown()
}

func (bs *BlogServer) shutdown() error {
	bs.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	<-ctx.Done()

	return nil
}

// I want to encapsulate starting the blog server so I can do runtime config from env vars
func StartBlogServer() error {
	bs, err := NewBlogServer(
		WithConfig("BLOG_"),
	)
	if err != nil {
		return err
	}

	err = bs.Start()
	if err != nil {
		return err
	}
	return nil
}
