// Command wireguard_exporter exposes WireGuard device and peer statistics as
// Prometheus metrics.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.zx2c4.com/wireguard/wgctrl"

	"github.com/donkeysharp/wireguard-exporter/internal/collector"
	"github.com/donkeysharp/wireguard-exporter/internal/peers"
)

// Set at build time via -ldflags.
var (
	version  = "dev"
	revision = "unknown"
)

// stringList is a repeatable flag that also accepts comma-separated values.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*s = append(*s, part)
		}
	}
	return nil
}

type config struct {
	listenAddress          string
	metricsPath            string
	interfaces             stringList
	peerNamesFile          string
	logLevel               string
	logFormat              string
	disableExporterMetrics bool
	showVersion            bool
}

func parseFlags() *config {
	cfg := &config{}
	flag.StringVar(&cfg.listenAddress, "web.listen-address", ":9586", "Address to listen on for HTTP requests.")
	flag.StringVar(&cfg.metricsPath, "web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	flag.Var(&cfg.interfaces, "wireguard.interface", "WireGuard interface to collect (repeatable or comma-separated). Default: all.")
	flag.StringVar(&cfg.peerNamesFile, "wireguard.peer-names-file", "", "Optional YAML file mapping peer public keys to names. Reloaded on SIGHUP.")
	flag.StringVar(&cfg.logLevel, "log.level", "info", "Log level: debug, info, warn, error.")
	flag.StringVar(&cfg.logFormat, "log.format", "logfmt", "Log format: logfmt or json.")
	flag.BoolVar(&cfg.disableExporterMetrics, "web.disable-exporter-metrics", false, "Exclude Go runtime and process metrics from the exporter.")
	flag.BoolVar(&cfg.showVersion, "version", false, "Print version information and exit.")
	flag.Parse()
	return cfg
}

func newLogger(level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid --log.level %q", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "logfmt":
		return slog.New(slog.NewTextHandler(os.Stderr, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stderr, opts)), nil
	default:
		return nil, fmt.Errorf("invalid --log.format %q", format)
	}
}

func main() {
	cfg := parseFlags()
	if cfg.showVersion {
		fmt.Printf("wireguard_exporter version=%s revision=%s go=%s\n", version, revision, runtime.Version())
		return
	}

	logger, err := newLogger(cfg.logLevel, cfg.logFormat)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	slog.SetDefault(logger)

	if err := run(cfg, logger); err != nil {
		logger.Error("exiting", "err", err)
		os.Exit(1)
	}
}

func run(cfg *config, logger *slog.Logger) error {
	logger.Info("starting wireguard_exporter", "version", version, "revision", revision, "go", runtime.Version())

	client, err := wgctrl.New()
	if err != nil {
		return fmt.Errorf("opening WireGuard control client: %w", err)
	}
	defer client.Close()

	var names *peers.Store
	if cfg.peerNamesFile != "" {
		names, err = peers.NewStore(cfg.peerNamesFile)
		if err != nil {
			return err
		}
		logger.Info("loaded peer names", "file", cfg.peerNamesFile, "count", names.Len())
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collector.New(client, collector.Options{
			Interfaces: cfg.interfaces,
			Names:      names,
			Logger:     logger,
		}),
		buildInfo(),
	)
	if !cfg.disableExporterMetrics {
		reg.MustRegister(
			collectors.NewGoCollector(),
			collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		)
	}

	mux := http.NewServeMux()
	mux.Handle(cfg.metricsPath, promhttp.InstrumentMetricHandler(reg, promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorLog:            slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ErrorHandling:       promhttp.ContinueOnError,
		MaxRequestsInFlight: 10,
		Timeout:             10 * time.Second,
	})))
	mux.HandleFunc("/-/healthy", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("OK\n"))
	})
	if cfg.metricsPath != "/" {
		mux.HandleFunc("/", landingPage(cfg.metricsPath))
	}

	srv := &http.Server{
		Addr:              cfg.listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go handleReload(ctx, names, logger)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "address", cfg.listenAddress, "path", cfg.metricsPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// handleReload reloads the peer names file whenever SIGHUP is received.
func handleReload(ctx context.Context, names *peers.Store, logger *slog.Logger) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			if names == nil {
				logger.Info("received SIGHUP, no peer names file configured")
				continue
			}
			if err := names.Reload(); err != nil {
				logger.Error("failed to reload peer names, keeping previous", "err", err)
				continue
			}
			logger.Info("reloaded peer names", "count", names.Len())
		}
	}
}

func buildInfo() prometheus.Collector {
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "wireguard_exporter_build_info",
		Help: "A metric with a constant '1' value labeled by version, revision and goversion.",
		ConstLabels: prometheus.Labels{
			"version":   version,
			"revision":  revision,
			"goversion": runtime.Version(),
		},
	})
	g.Set(1)
	return g
}

func landingPage(metricsPath string) http.HandlerFunc {
	page := []byte(`<html>
<head><title>WireGuard Exporter</title></head>
<body>
<h1>WireGuard Exporter</h1>
<p><a href="` + html.EscapeString(metricsPath) + `">Metrics</a></p>
</body>
</html>
`)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	}
}
