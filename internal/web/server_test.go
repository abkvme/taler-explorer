package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"taler-explorer/internal/config"
	"taler-explorer/internal/store"
)

// TestServerRoutes smokes every page + partial against an in-memory SQLite so
// template runtime errors (silent in go build) surface as test failures.
// newTestHandler builds a server over an in-memory SQLite for the tests below.
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := &config.Config{
		UI:           config.UIConfig{SiteName: "Taler Explorer", CoinSymbol: "TLR", AccentColor: "#c9a24b"},
		Movements:    config.MovementsConfig{ThresholdLow: 100, ThresholdMid: 1000, ThresholdHigh: 5000},
		Transactions: config.TransactionsConfig{PerPage: 100, GreenBelow: 100, YellowBelow: 1000, RedBelow: 10000},
		Network:      config.NetworkConfig{HistoryHours: 24},
		Prices:       config.PricesConfig{Enabled: true, Pair: "tlr_usdt", QuoteSymbol: "USDT", PollIntervalMin: 10, MarketURL: "https://qutrade.io/en/?market=tlr_usdt"},
		Server:       config.ServerConfig{Listen: "127.0.0.1:0"},
		DB:           config.DBConfig{Path: ":memory:"},
	}

	srv, err := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, "test")
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	return srv.Handler()
}

func TestServerRoutes(t *testing.T) {
	h := newTestHandler(t)

	cases := []string{
		"/",
		"/txs",
		"/txs?page=2",
		"/movements",
		"/network",
		"/about",
		"/address/TestAddr1234",
		"/_partial/header-stats",
		"/_partial/latest-blocks",
		"/_partial/latest-tx",
		"/_partial/movements",
		"/_partial/peers",
		"/_partial/hashrate-series",
		"/sitemap.xml",
	}
	for _, url := range cases {
		t.Run(url, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != 200 {
				t.Fatalf("GET %s: status=%d body=%s", url, rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			if strings.Contains(body, "<no value>") {
				t.Fatalf("GET %s: template leak <no value>", url)
			}
		})
	}
}

// TestAboutPage checks the About page serves its content and its banner, and
// that the banner is actually in the embedded asset tree - a missing file would
// still render a 200 with a broken image, which the route smoke test above
// cannot see.
func TestAboutPage(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/about", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("GET /about: status=%d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"/static/img/taler-banner.jpg",
		"https://taler.tech/",
		"https://t.me/talercommunity",
		"https://github.com/abkvme/taler/releases",
		`data-t="About"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /about: missing %q", want)
		}
	}

	img := httptest.NewRequest(http.MethodGet, "/static/img/taler-banner.jpg", nil)
	imgRR := httptest.NewRecorder()
	h.ServeHTTP(imgRR, img)
	if imgRR.Code != 200 {
		t.Fatalf("GET banner: status=%d", imgRR.Code)
	}
	if imgRR.Body.Len() < 1024 {
		t.Errorf("GET banner: only %d bytes, expected a real image", imgRR.Body.Len())
	}
}

// TestStatTilesScrollAway guards the layout change that moved the stat tiles
// out of the sticky header. Inside it, six tiles stayed pinned to the top of
// every page and buried the data below them.
func TestStatTilesScrollAway(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/network", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	body := rr.Body.String()

	headerEnd := strings.Index(body, "</header>")
	stats := strings.Index(body, `id="header-stats"`)
	if headerEnd < 0 || stats < 0 {
		t.Fatalf("layout markers not found (header=%d stats=%d)", headerEnd, stats)
	}
	if stats < headerEnd {
		t.Error("the stat tiles are back inside the sticky header; they must follow it")
	}
}
