package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"taler-explorer/internal/config"
	"taler-explorer/internal/geoip"
	"taler-explorer/internal/indexer"
	"taler-explorer/internal/prices"
	"taler-explorer/internal/rpc"
	"taler-explorer/internal/store"
	"taler-explorer/internal/web"
)

// version is baked in at build time via `-ldflags "-X main.version=..."`.
// Tag-driven release workflow sets this to the git tag (e.g. v0.1.0).
// Local `./run.sh` dev builds set it from `git describe --tags --always --dirty`.
var version = "dev"

func main() {
	cfgPath := flag.String("config", "config.toml", "path to config TOML")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		println(version)
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("load config", "err", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.DB.Path)
	if err != nil {
		logger.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	// Self-check log: if peers_in_db drops to 0 across restarts the operator
	// instantly knows persistence is broken (e.g. relative DB path landing
	// outside the mounted volume).
	if abs, aerr := filepath.Abs(cfg.DB.Path); aerr == nil {
		n, _ := st.CountPeersActiveSince(context.Background(), 0)
		logger.Info("store opened", "path", abs, "peers_in_db", n)
	}

	client := rpc.New(cfg.RPC.URL, cfg.RPC.User, cfg.RPC.Password, cfg.RPC.Timeout())

	// Probe the node once so startup fails loudly if RPC creds are wrong.
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), cfg.RPC.Timeout())
	if _, err := client.GetBlockCount(probeCtx); err != nil {
		logger.Warn("initial RPC probe failed; workers will retry", "err", err)
	}
	cancelProbe()

	geo := geoip.New(st, cfg.GeoIP.Endpoint, cfg.GeoIP.Enabled)

	syncer := &indexer.Syncer{
		RPC:             client,
		Store:           st,
		Log:             logger.With("worker", "syncer"),
		Interval:        time.Duration(cfg.Indexer.TipPollSeconds) * time.Second,
		InitialBackfill: cfg.Indexer.InitialBackfill,
	}
	mempool := &indexer.Mempool{
		RPC:      client,
		Store:    st,
		Log:      logger.With("worker", "mempool"),
		Interval: time.Duration(cfg.Indexer.MempoolPollSeconds) * time.Second,
	}
	peers := &indexer.Peers{
		RPC:         client,
		Store:       st,
		Log:         logger.With("worker", "peers"),
		Interval:    time.Duration(cfg.Indexer.PeersPollSeconds) * time.Second,
		GeoIP:       geo,
		RetainHours: cfg.Network.HistoryHours,
	}
	stats := &indexer.Stats{
		RPC:                client,
		Store:              st,
		Log:                logger.With("worker", "stats"),
		Interval:           time.Duration(cfg.Indexer.StatsPollSeconds) * time.Second,
		SupplyRefreshEvery: 15 * time.Minute,
	}

	server, err := web.New(cfg, st, logger.With("component", "web"), client, version)
	if err != nil {
		logger.Error("build web", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go run("syncer",  syncer.Run,  rootCtx, logger)
	go run("mempool", mempool.Run, rootCtx, logger)
	go run("peers",   peers.Run,   rootCtx, logger)
	go run("stats",   stats.Run,   rootCtx, logger)

	if cfg.Prices.Enabled {
		priceWorker := &prices.Worker{
			Client:   prices.NewClient(cfg.Prices.Endpoint),
			Store:    st,
			Log:      logger.With("worker", "prices"),
			Pair:     cfg.Prices.Pair,
			Interval: time.Duration(cfg.Prices.PollIntervalMin) * time.Minute,
		}
		go run("prices", priceWorker.Run, rootCtx, logger)
	}

	go func() {
		logger.Info("http listening", "addr", cfg.Server.Listen)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http", "err", err)
			cancel()
		}
	}()

	<-rootCtx.Done()
	logger.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func run(name string, fn func(context.Context) error, ctx context.Context, logger *slog.Logger) {
	if err := fn(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("worker stopped", "worker", name, "err", err)
	}
}
