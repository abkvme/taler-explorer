package web

import (
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"taler-explorer/internal/config"
	"taler-explorer/internal/rpc"
	"taler-explorer/internal/store"
	"taler-explorer/internal/web/handlers"
)

//go:embed templates templates/partials static
var embedded embed.FS

type Server struct {
	cfg   *config.Config
	store *store.Store
	log   *slog.Logger
	tpl   *handlers.Templates
	rpc   *rpc.Client
	mux   http.Handler
}

func New(cfg *config.Config, s *store.Store, log *slog.Logger, rpcClient *rpc.Client) (*Server, error) {
	tpl, err := handlers.LoadTemplates(embedded, cfg.UI)
	if err != nil {
		return nil, err
	}
	srv := &Server{cfg: cfg, store: s, log: log, tpl: tpl, rpc: rpcClient}
	srv.mux = srv.buildRouter()
	return srv, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) buildRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))

	staticFS, err := fs.Sub(embedded, "static")
	if err != nil {
		panic(err)
	}
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	// Common root-level asset: favicon.
	r.Get("/favicon.ico", func(w http.ResponseWriter, req *http.Request) {
		http.ServeFileFS(w, req, staticFS, "favicon.ico")
	})

	h := &handlers.Handlers{Store: s.store, Tpl: s.tpl, Cfg: s.cfg, Log: s.log, RPC: s.rpc}

	r.Get("/", h.Blocks)
	r.Get("/txs", h.Transactions)
	r.Get("/blocks/{height}", h.BlockDetail)
	r.Get("/txs/{txid}", h.TxDetail)
	r.Get("/address/{addr}", h.AddressDetail)
	r.Get("/search", h.Search)
	r.Get("/robots.txt", h.Robots)
	r.Get("/sitemap.xml", h.Sitemap)
	r.Get("/movements", h.Movements)
	r.Get("/network", h.Network)

	// Private partials consumed by HTMX/polling from the browser.
	r.Get("/_partial/header-stats", h.HeaderStats)
	r.Get("/_partial/latest-blocks", h.LatestBlocksPartial)
	r.Get("/_partial/latest-tx", h.LatestTxPartial)
	r.Get("/_partial/movements", h.MovementsPartial)
	r.Get("/_partial/peers", h.PeersPartial)
	r.Get("/_partial/hashrate-series", h.HashrateSeries)
	r.Get("/_partial/price-series", h.PriceSeries)

	// 404 fallback
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	return r
}

// Ensure Go doesn't flag html/template as unused if a future refactor moves it.
var _ = template.FuncMap{}
