// Package server wires storage, ingestion, the query engine, the HTTP API
// and the UI into a running TinyObs server.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nicktill/tinyobs/pkg/api"
	"github.com/nicktill/tinyobs/pkg/otlp"
	"github.com/nicktill/tinyobs/pkg/promql"
	"github.com/nicktill/tinyobs/pkg/scrape"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

// Version is the TinyObs version, set at build time with -ldflags.
var Version = "2.0.0-dev"

// Config configures a Server.
type Config struct {
	Listen     string // UI and API, e.g. "127.0.0.1:8421"
	OTLPListen string // extra OTLP-only listener, e.g. "127.0.0.1:4318"; "" disables it
	DataDir    string
	Retention  time.Duration
	MaxSeries  int
	MemoryMB   int64
	UI         fs.FS // static UI files; nil serves no UI
	Logger     *slog.Logger

	ScrapeTargets  []scrape.Target
	ScrapeInterval time.Duration

	// AuthToken, if set, is required on every request except health checks,
	// as a bearer token or as the password of HTTP Basic auth.
	AuthToken string
	// TLSCert and TLSKey enable HTTPS on both listeners.
	TLSCert, TLSKey string
}

// Server is a TinyObs instance.
type Server struct {
	cfg    Config
	log    *slog.Logger
	db     *tsdb.DB
	engine *promql.Engine
	scrape *scrape.Manager
	self   *selfMonitor
	http   *http.Server
	otlp   *http.Server
}

// New opens storage and builds the HTTP handlers.
func New(cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.ScrapeInterval <= 0 {
		cfg.ScrapeInterval = 15 * time.Second
	}
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		return nil, errors.New("TLS needs both a certificate and a key")
	}
	db, err := tsdb.Open(tsdb.Options{
		Dir:       cfg.DataDir,
		Retention: cfg.Retention,
		MaxSeries: cfg.MaxSeries,
		MemoryMB:  cfg.MemoryMB,
	})
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:    cfg,
		log:    cfg.Logger,
		db:     db,
		engine: promql.NewEngine(db, promql.EngineOptions{}),
		scrape: scrape.NewManager(db, cfg.ScrapeTargets, cfg.ScrapeInterval, 10*time.Second, cfg.Logger),
		self:   newSelfMonitor(db, cfg.Listen),
	}
	s.http = &http.Server{
		Handler:           s.self.instrument(s.auth(s.routes())),
		ReadHeaderTimeout: 10 * time.Second,
		// Range queries can legitimately take a while; the engine has its own timeout.
		WriteTimeout: 2 * time.Minute,
	}
	if cfg.OTLPListen != "" {
		mux := http.NewServeMux()
		mux.Handle("POST /v1/metrics", &otlp.Handler{DB: db})
		s.otlp = &http.Server{Handler: s.self.instrument(s.auth(mux)), ReadHeaderTimeout: 10 * time.Second, WriteTimeout: time.Minute}
	}
	return s, nil
}

// DB returns the server's storage. For tests and embedding.
func (s *Server) DB() *tsdb.DB { return s.db }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	(&api.API{DB: s.db, Engine: s.engine, BuildInfo: api.BuildInfo{Version: Version}, Targets: s.targets}).Register(mux)
	mux.Handle("POST /v1/metrics", &otlp.Handler{DB: s.db})
	mux.Handle("GET /metrics", s.self)
	mux.HandleFunc("POST /api/v1/admin/tsdb/snapshot", s.snapshot)
	mux.HandleFunc("GET /api/v1/status/config", s.statusConfig)
	mux.HandleFunc("GET /-/healthy", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "TinyObs is Healthy.")
	})
	mux.HandleFunc("GET /-/ready", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "TinyObs is Ready.")
	})
	if s.cfg.UI != nil {
		files := http.FileServerFS(s.cfg.UI)
		mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The UI is small and changes with the binary; let browsers revalidate.
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r)
		}))
	}
	return mux
}

// auth enforces the token, if one is configured. Health checks stay open so
// load balancers and container runtimes can probe without credentials.
func (s *Server) auth(next http.Handler) http.Handler {
	if s.cfg.AuthToken == "" {
		return next
	}
	want := []byte(s.cfg.AuthToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/-/healthy" || r.URL.Path == "/-/ready" {
			next.ServeHTTP(w, r)
			return
		}
		var got string
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			got = strings.TrimPrefix(h, "Bearer ")
		} else if _, pass, ok := r.BasicAuth(); ok {
			got = pass
		}
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="TinyObs", charset="UTF-8"`)
			http.Error(w, "unauthorized: send the TinyObs auth token as a bearer token or Basic auth password", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// snapshot writes a backup under <data>/snapshots and returns its name, like
// Prometheus's TSDB snapshot API.
func (s *Server) snapshot(w http.ResponseWriter, _ *http.Request) {
	fail := func(err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"status": "error", "errorType": "internal", "error": err.Error()})
	}
	dir := filepath.Join(s.cfg.DataDir, "snapshots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail(err)
		return
	}
	name := time.Now().UTC().Format("20060102T150405.000Z") + ".tinyobs"
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		fail(err)
		return
	}
	err = s.db.Snapshot(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		fail(err)
		return
	}
	s.log.Info("snapshot written", "path", path)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]string{"name": name, "path": path}})
}

// statusConfig reports the effective configuration for the UI's System page.
func (s *Server) statusConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{
		"listen":         s.cfg.Listen,
		"otlpListen":     s.cfg.OTLPListen,
		"dataDir":        s.cfg.DataDir,
		"retention":      s.cfg.Retention.String(),
		"maxSeries":      s.db.Stats().MaxSeries,
		"scrapeInterval": s.cfg.ScrapeInterval.String(),
		"auth":           s.cfg.AuthToken != "",
		"tls":            s.cfg.TLSCert != "",
		"version":        Version,
	}})
}

func (s *Server) targets() []api.Target {
	var out []api.Target
	for _, st := range s.scrape.Status() {
		out = append(out, api.Target{
			Labels:             map[string]string{"job": st.Job, "instance": st.Instance()},
			DiscoveredLabels:   map[string]string{"__address__": st.Instance(), "__metrics_path__": st.URL.Path, "__scheme__": st.URL.Scheme, "job": st.Job},
			ScrapePool:         st.Job,
			ScrapeURL:          st.URL.String(),
			GlobalURL:          st.URL.String(),
			LastError:          st.LastError,
			LastScrape:         st.LastScrape,
			LastScrapeDuration: st.LastDuration.Seconds(),
			Health:             st.Health,
			ScrapeInterval:     st.Interval.String(),
			ScrapeTimeout:      st.Timeout.String(),
		})
	}
	return out
}

// Addrs are the addresses Run listens on. OTLP is nil when that listener is
// disabled or its port was unavailable.
type Addrs struct {
	HTTP, OTLP net.Addr
}

// Run serves until ctx is cancelled, then shuts down gracefully: in-flight
// requests finish, background work stops and storage is closed. ready, if
// not nil, receives the bound addresses once the server accepts connections.
func (s *Server) Run(ctx context.Context, ready chan<- Addrs) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		s.db.Close()
		return err
	}
	var addrs Addrs
	addrs.HTTP = ln.Addr()
	var otlpLn net.Listener
	if s.otlp != nil {
		// The OTLP port is a convenience; if it is taken (say, by a local
		// OpenTelemetry Collector) OTLP still works on the main port.
		if otlpLn, err = net.Listen("tcp", s.cfg.OTLPListen); err != nil {
			s.log.Warn("OTLP port unavailable; OTLP is still served on the main port at /v1/metrics", "addr", s.cfg.OTLPListen, "err", err)
			s.otlp = nil
		} else {
			addrs.OTLP = otlpLn.Addr()
		}
	}

	var wg sync.WaitGroup
	bg, stopBG := context.WithCancel(context.Background())
	for _, f := range []func(context.Context){s.retentionLoop, s.scrape.Run, s.selfMonitorLoop} {
		wg.Add(1)
		go func(f func(context.Context)) {
			defer wg.Done()
			f(bg)
		}(f)
	}

	serve := func(srv *http.Server, l net.Listener) error {
		if s.cfg.TLSCert != "" {
			return srv.ServeTLS(l, s.cfg.TLSCert, s.cfg.TLSKey)
		}
		return srv.Serve(l)
	}
	errc := make(chan error, 2)
	go func() { errc <- serve(s.http, ln) }()
	if s.otlp != nil {
		go func() { errc <- serve(s.otlp, otlpLn) }()
	}
	if ready != nil {
		ready <- addrs
	}

	select {
	case <-ctx.Done():
		err = nil
	case err = <-errc:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, srv := range []*http.Server{s.http, s.otlp} {
		if srv != nil {
			if serr := srv.Shutdown(shutdownCtx); serr != nil {
				s.log.Warn("http shutdown", "err", serr)
			}
		}
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
	s.every(ctx, time.Hour, func() {
		if err := s.db.ApplyRetention(); err != nil {
			s.log.Error("retention failed", "err", err)
		}
	})
}

// selfMonitorLoop records TinyObs's own metrics every 15 seconds.
func (s *Server) selfMonitorLoop(ctx context.Context) {
	s.every(ctx, 15*time.Second, func() {
		if err := s.self.record(time.Now()); err != nil {
			s.log.Error("recording self-metrics failed", "err", err)
		}
	})
}

// every runs f now and then at each interval until ctx is done.
func (s *Server) every(ctx context.Context, d time.Duration, f func()) {
	f()
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f()
		}
	}
}
