// Package server wires storage, the query engine and the HTTP API into a
// running TinyObs server.
package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/nicktill/tinyobs/pkg/api"
	"github.com/nicktill/tinyobs/pkg/promql"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

// Version is the TinyObs version, set at build time with -ldflags.
var Version = "2.0.0"

// Config configures a Server.
type Config struct {
	Listen    string // address to listen on, e.g. "127.0.0.1:8080"
	DataDir   string
	Retention time.Duration
	MaxSeries int
	// MaxSeriesPerMetric caps series per metric name; see tsdb.Options.
	MaxSeriesPerMetric int
	MemoryMB           int64
	WebDir             string // static UI files
	// AuthToken, when set, is required as "Authorization: Bearer <token>"
	// on the write endpoints. Reads stay open so the dashboard and Grafana
	// work unchanged; bind to localhost or a private network to protect them.
	AuthToken string
	Logger    *slog.Logger
}

// Server is a running TinyObs instance.
type Server struct {
	cfg    Config
	log    *slog.Logger
	db     *tsdb.DB
	engine *promql.Engine
	http   *http.Server
}

// New opens storage and builds the HTTP handlers.
func New(cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	db, err := tsdb.Open(tsdb.Options{
		Dir:                cfg.DataDir,
		Retention:          cfg.Retention,
		MaxSeries:          cfg.MaxSeries,
		MaxSeriesPerMetric: cfg.MaxSeriesPerMetric,
		MemoryMB:           cfg.MemoryMB,
	})
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:    cfg,
		log:    cfg.Logger,
		db:     db,
		engine: promql.NewEngine(db, promql.EngineOptions{}),
	}
	s.http = &http.Server{
		Addr:              cfg.Listen,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// Range queries can legitimately take a while; the engine has its own timeout.
		WriteTimeout: 2 * time.Minute,
	}
	return s, nil
}

// DB returns the server's storage. For tests and embedding.
func (s *Server) DB() *tsdb.DB { return s.db }

// Handler returns the server's HTTP handler. For tests.
func (s *Server) Handler() http.Handler { return s.http.Handler }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	(&api.API{DB: s.db, Engine: s.engine, BuildInfo: api.BuildInfo{Version: Version}, Auth: s.requireToken}).Register(mux)
	mux.HandleFunc("GET /-/healthy", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "TinyObs is Healthy.")
	})
	mux.HandleFunc("GET /-/ready", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "TinyObs is Ready.")
	})
	mux.HandleFunc("GET /metrics", s.selfMetrics)
	mux.HandleFunc("POST /v1/ingest", s.requireToken(s.ingest))
	if s.cfg.WebDir != "" {
		files := http.FileServer(http.Dir(s.cfg.WebDir))
		mux.Handle("GET /web/", http.StripPrefix("/web/", files))
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, s.cfg.WebDir+"/dashboard.html")
		})
	}
	return mux
}

// requireToken wraps a write handler with the bearer token check, when a
// token is configured.
func (s *Server) requireToken(h http.HandlerFunc) http.HandlerFunc {
	if s.cfg.AuthToken == "" {
		return h
	}
	want := []byte("Bearer " + s.cfg.AuthToken)
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tinyobs"`)
			http.Error(w, "missing or invalid bearer token", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// Run serves HTTP and runs background work until ctx is cancelled, then shuts
// down gracefully: in-flight requests finish and storage is closed.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		s.db.Close()
		return err
	}
	s.log.Info("listening", "addr", ln.Addr().String())

	var wg sync.WaitGroup
	bg, stopBG := context.WithCancel(context.Background())
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.retentionLoop(bg)
	}()

	errc := make(chan error, 1)
	go func() { errc <- s.http.Serve(ln) }()

	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if serr := s.http.Shutdown(shutdownCtx); serr != nil {
		s.log.Warn("http shutdown", "err", serr)
	}
	stopBG()
	wg.Wait()
	if cerr := s.db.Close(); cerr != nil {
		s.log.Error("closing storage", "err", cerr)
	}
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// retentionLoop applies retention at startup and then hourly.
func (s *Server) retentionLoop(ctx context.Context) {
	apply := func() {
		start := time.Now()
		if err := s.db.ApplyRetention(); err != nil {
			s.log.Error("retention failed", "err", err)
			return
		}
		s.log.Debug("retention applied", "took", time.Since(start).Round(time.Millisecond))
	}
	apply()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			apply()
		}
	}
}
