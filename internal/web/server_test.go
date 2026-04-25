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
func TestServerRoutes(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		UI:           config.UIConfig{SiteName: "Taler Explorer", CoinSymbol: "TLR", AccentColor: "#c9a24b"},
		Movements:    config.MovementsConfig{ThresholdLow: 100, ThresholdMid: 1000, ThresholdHigh: 5000},
		Transactions: config.TransactionsConfig{PerPage: 100, GreenBelow: 100, YellowBelow: 1000, RedBelow: 10000},
		Network:      config.NetworkConfig{HistoryHours: 24},
		Prices:       config.PricesConfig{Enabled: true, Pair: "tlr_usdt", QuoteSymbol: "USDT", PollIntervalMin: 10, MarketURL: "https://qutrade.io/en/?market=tlr_usdt"},
		Server:       config.ServerConfig{Listen: "127.0.0.1:0"},
		DB:           config.DBConfig{Path: ":memory:"},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv, err := New(cfg, st, logger, nil, nil, "test")
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	h := srv.Handler()

	cases := []string{
		"/",
		"/txs",
		"/txs?page=2",
		"/movements",
		"/network",
		"/address/TestAddr1234",
		"/_partial/header-stats",
		"/_partial/latest-blocks",
		"/_partial/latest-tx",
		"/_partial/movements",
		"/_partial/peers",
		"/_partial/hashrate-series",
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
