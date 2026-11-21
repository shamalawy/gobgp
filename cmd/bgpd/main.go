// Command bgpd is a BGP-4 daemon with an embedded web UI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/shamalawy/gobgp/internal/api"
	"github.com/shamalawy/gobgp/internal/bgp"
	"github.com/shamalawy/gobgp/internal/config"
	"github.com/shamalawy/gobgp/web"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	var (
		dataDir  = flag.String("data-dir", "data", "directory for the configuration file and uploaded dumps")
		httpAddr = flag.String("http", "127.0.0.1:8080", "address of the web UI, REST API and /metrics; it has no authentication")
		grpcAddr = flag.String("grpc", "", "address of GoBGP's native gRPC API, e.g. 127.0.0.1:50051 (disabled when empty)")
		seed     = flag.String("seed", "", "configuration file copied into the data directory on the first start")
		asn      = flag.Uint("asn", 64512, "local AS number for a first start without -seed")
		routerID = flag.String("router-id", "", "router ID for a first start without -seed (default: first IPv4 address)")
		logLevel = flag.String("log-level", "info", "debug, info, warn or error")
		showVer  = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("bgpd", version)
		return
	}
	if err := run(*dataDir, *httpAddr, *grpcAddr, *seed, uint32(*asn), *routerID, *logLevel); err != nil {
		fmt.Fprintln(os.Stderr, "bgpd:", err)
		os.Exit(1)
	}
}

func run(dataDir, httpAddr, grpcAddr, seed string, asn uint32, routerID, logLevel string) error {
	var level slog.Level
	if err := level.UnmarshalText([]byte(logLevel)); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if routerID == "" {
		routerID = firstIPv4()
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return err
	}
	path := filepath.Join(dataDir, "bgpd.yaml")
	if _, err := os.Stat(path); seed != "" && errors.Is(err, fs.ErrNotExist) {
		b, err := os.ReadFile(seed)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return err
		}
	}
	store, err := config.Open(path, config.Default(asn, routerID))
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	engine := bgp.New(store, log, grpcAddr)
	ui := web.UI()
	if ui == nil {
		log.Warn("the web UI is not built into this binary; serving the API only")
	}
	srv := api.New(engine, log, dataDir, ui, version)
	if err := engine.Start(ctx); err != nil {
		return err
	}
	defer engine.Stop()
	go srv.Run(ctx)

	httpSrv := &http.Server{Addr: httpAddr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()
	cfg := store.Get().Global
	log.Info("bgpd started", "version", version, "asn", cfg.ASN, "router-id", cfg.RouterID, "http", httpAddr, "grpc", grpcAddr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err // open event streams are cut at the deadline, which is fine
	}
	return nil
}

// firstIPv4 picks a router ID for a first start without one.
func firstIPv4() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			return n.IP.String()
		}
	}
	return "127.0.0.1"
}
