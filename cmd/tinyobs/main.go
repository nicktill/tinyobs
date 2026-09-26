// Command tinyobs runs the TinyObs metrics server.
//
//	tinyobs [flags]              run the server
//	tinyobs restore FILE [flags] restore a snapshot into an empty data directory
//	tinyobs version              print the version
//
// Every flag can also be set with an environment variable, shown in -help.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/nicktill/tinyobs/pkg/scrape"
	"github.com/nicktill/tinyobs/pkg/server"
	"github.com/nicktill/tinyobs/pkg/tsdb"
	"github.com/nicktill/tinyobs/web"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-version":
			fmt.Println("tinyobs", server.Version)
			return
		case "restore":
			os.Exit(restore(os.Args[2:]))
		}
	}
	os.Exit(run(os.Args[1:]))
}

// scrapeFlag collects repeated -scrape values.
type scrapeFlag []scrape.Target

func (s *scrapeFlag) String() string { return "" }
func (s *scrapeFlag) Set(v string) error {
	for _, spec := range strings.Split(v, ",") {
		if spec = strings.TrimSpace(spec); spec == "" {
			continue
		}
		t, err := scrape.ParseTarget(spec)
		if err != nil {
			return err
		}
		*s = append(*s, t)
	}
	return nil
}

func defaultDataDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".tinyobs")
	}
	return "tinyobs-data"
}

// envOr returns the environment variable's value, or def.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run(args []string) int {
	fs := flag.NewFlagSet("tinyobs", flag.ContinueOnError)
	var (
		listen      = fs.String("listen", envOr("TINYOBS_LISTEN", "127.0.0.1:8421"), "`address` for the UI and API ($TINYOBS_LISTEN)")
		otlpListen  = fs.String("otlp-listen", envOr("TINYOBS_OTLP_LISTEN", "127.0.0.1:4318"), "`address` for an extra OTLP/HTTP listener on the standard OTLP port, so OpenTelemetry SDKs work unconfigured; empty disables it ($TINYOBS_OTLP_LISTEN)")
		dataDir     = fs.String("data", envOr("TINYOBS_DATA_DIR", defaultDataDir()), "data `directory` ($TINYOBS_DATA_DIR)")
		retention   = fs.Duration("retention", 72*time.Hour, "how long to keep samples ($TINYOBS_RETENTION)")
		maxSeries   = fs.Int("max-series", 50_000, "series limit; bounds memory and disk ($TINYOBS_MAX_SERIES)")
		memoryMB    = fs.Int64("memory-mb", 64, "storage cache budget in MB ($TINYOBS_MAX_MEMORY_MB)")
		scrapeEvery = fs.Duration("scrape-interval", 15*time.Second, "scrape interval ($TINYOBS_SCRAPE_INTERVAL)")
		authToken   = fs.String("auth-token", os.Getenv("TINYOBS_AUTH_TOKEN"), "require this token (bearer or Basic auth password) on every request but health checks ($TINYOBS_AUTH_TOKEN)")
		tlsCert     = fs.String("tls-cert", os.Getenv("TINYOBS_TLS_CERT"), "TLS certificate `file` ($TINYOBS_TLS_CERT)")
		tlsKey      = fs.String("tls-key", os.Getenv("TINYOBS_TLS_KEY"), "TLS key `file` ($TINYOBS_TLS_KEY)")
		open        = fs.Bool("open", false, "open the UI in a browser once started")
		logLevel    = fs.String("log-level", envOr("TINYOBS_LOG_LEVEL", "info"), "debug, info, warn or error ($TINYOBS_LOG_LEVEL)")
		targets     scrapeFlag
	)
	fs.Var(&targets, "scrape", "`target` to scrape, as [job=]host:port[/path]; repeatable or comma-separated ($TINYOBS_SCRAPE)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "TinyObs %s: a single-binary metrics server.\n\nUsage:\n  tinyobs [flags]\n  tinyobs restore SNAPSHOT [-data DIR]\n  tinyobs version\n\nFlags:\n", server.Version)
		fs.PrintDefaults()
	}

	// Duration and number flags take their environment defaults here, so an
	// invalid value is reported like an invalid flag.
	for flagName, env := range map[string]string{
		"retention": "TINYOBS_RETENTION", "max-series": "TINYOBS_MAX_SERIES", "memory-mb": "TINYOBS_MAX_MEMORY_MB",
		"scrape-interval": "TINYOBS_SCRAPE_INTERVAL", "scrape": "TINYOBS_SCRAPE",
	} {
		if v := os.Getenv(env); v != "" {
			if err := fs.Set(flagName, v); err != nil {
				fmt.Fprintf(os.Stderr, "invalid $%s: %v\n", env, err)
				return 2
			}
		}
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		fmt.Fprintf(os.Stderr, "invalid -log-level %q\n", *logLevel)
		return 2
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	if *retention <= 0 || *maxSeries <= 0 || *memoryMB <= 0 || *scrapeEvery <= 0 {
		log.Error("-retention, -max-series, -memory-mb and -scrape-interval must be positive")
		return 2
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		log.Error("creating data directory", "err", err)
		return 1
	}

	srv, err := server.New(server.Config{
		Listen:         *listen,
		OTLPListen:     *otlpListen,
		DataDir:        *dataDir,
		Retention:      *retention,
		MaxSeries:      *maxSeries,
		MemoryMB:       *memoryMB,
		UI:             web.FS(),
		Logger:         log,
		ScrapeTargets:  targets,
		ScrapeInterval: *scrapeEvery,
		AuthToken:      *authToken,
		TLSCert:        *tlsCert,
		TLSKey:         *tlsKey,
	})
	if err != nil {
		log.Error("starting TinyObs", "err", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ready := make(chan server.Addrs, 1)
	errc := make(chan error, 1)
	go func() { errc <- srv.Run(ctx, ready) }()

	select {
	case err := <-errc:
		log.Error("TinyObs stopped", "err", err)
		return 1
	case addrs := <-ready:
		scheme := "http"
		if *tlsCert != "" {
			scheme = "https"
		}
		uiURL := fmt.Sprintf("%s://%s", scheme, browsable(addrs.HTTP))
		printBanner(uiURL, addrs, scheme, *dataDir, *retention, targets, *authToken != "")
		if !isLoopback(addrs.HTTP) && *authToken == "" {
			log.Warn("listening on a non-loopback address without -auth-token: anyone who can reach this port can read and write metrics")
		}
		if *open {
			openBrowser(uiURL)
		}
	}
	if err := <-errc; err != nil {
		log.Error("TinyObs stopped", "err", err)
		return 1
	}
	log.Info("TinyObs stopped")
	return 0
}

func printBanner(uiURL string, addrs server.Addrs, scheme, dataDir string, retention time.Duration, targets []scrape.Target, auth bool) {
	otlpURL := uiURL + "/v1/metrics"
	if addrs.OTLP != nil {
		otlpURL = fmt.Sprintf("%s://%s/v1/metrics", scheme, browsable(addrs.OTLP))
	}
	fmt.Fprintf(os.Stderr, "\n  TinyObs %s\n\n", server.Version)
	fmt.Fprintf(os.Stderr, "  UI and API   %s\n", uiURL)
	fmt.Fprintf(os.Stderr, "  OTLP         %s\n", otlpURL)
	fmt.Fprintf(os.Stderr, "  Data         %s (keeping %s)\n", dataDir, shortDuration(retention))
	for _, t := range targets {
		fmt.Fprintf(os.Stderr, "  Scraping     %s  job=%s\n", t.URL, t.Job)
	}
	if auth {
		fmt.Fprintf(os.Stderr, "  Auth         token required\n")
	}
	fmt.Fprintln(os.Stderr)
}

// shortDuration formats 72h0m0s as 72h and 1m30s as 1m30s.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") { // "5m0s" -> "5m"
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") { // "72h0m" -> "72h"
		s = s[:len(s)-2]
	}
	return s
}

// browsable turns a wildcard listen address into one a browser can open.
func browsable(a net.Addr) string {
	host, port, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

func isLoopback(a net.Addr) bool {
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func restore(args []string) int {
	fs := flag.NewFlagSet("tinyobs restore", flag.ContinueOnError)
	dataDir := fs.String("data", envOr("TINYOBS_DATA_DIR", defaultDataDir()), "data `directory` to restore into; must be empty")
	if err := fs.Parse(reorder(args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: tinyobs restore SNAPSHOT [-data DIR]")
		return 2
	}
	f, err := os.Open(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer f.Close()
	if err := tsdb.Restore(*dataDir, f); err != nil {
		fmt.Fprintln(os.Stderr, "restore failed:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "Restored %s into %s\n", fs.Arg(0), *dataDir)
	return 0
}

// reorder moves flags before positional arguments, so both
// "restore FILE -data DIR" and "restore -data DIR FILE" work.
func reorder(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			if !strings.Contains(args[i], "=") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		rest = append(rest, args[i])
	}
	return append(flags, rest...)
}
