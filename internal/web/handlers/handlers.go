package handlers

import (
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"taler-explorer/internal/config"
	"taler-explorer/internal/rpc"
	"taler-explorer/internal/store"
)

type Handlers struct {
	Store *store.Store
	Tpl   *Templates
	Cfg   *config.Config
	Log   *slog.Logger
	RPC   *rpc.Client // optional; used as fallback when the DB lacks tx inputs
}

type pageData struct {
	Title       string
	Active      string // "blocks" | "txs" | "movements" | "network" | "block" | "tx"
	UI          config.UIConfig
	HeaderStats *headerStatsView
	Body        any
}

type headerStatsView struct {
	HashrateHPS   float64
	DifficultyPOW float64
	DifficultyPOS float64
	Supply        float64
	Height        int64
	PeerCount     int
	Stale         bool
	UpdatedAgo    string

	// Price is populated when [prices] is enabled and we have at least one
	// snapshot. PriceAvailable=false hides the tile.
	PriceAvailable bool
	Price          float64
	PriceTrend     float64
	PriceLow       float64
	PriceHigh      float64
	PriceQuote     string
	PriceMarketURL string
	PriceAgeMin    int

	// Market cap (= Supply * Price) — rendered as a separate tile when both
	// supply and price are known. Uses PriceQuote/PriceMarketURL above.
	MarketCapAvailable bool
	MarketCap          float64
}

func (h *Handlers) newHeaderStats(ctx context.Context) *headerStatsView {
	v := &headerStatsView{Stale: true, UpdatedAgo: "never"}
	if snap, err := h.Store.LatestStats(ctx); err == nil && snap != nil {
		age := time.Since(time.Unix(snap.TS, 0))
		v = &headerStatsView{
			HashrateHPS:   snap.HashrateHPS,
			DifficultyPOW: snap.DifficultyPOW,
			DifficultyPOS: snap.DifficultyPOS,
			Supply:        snap.Supply,
			Height:        snap.Height,
			PeerCount:     snap.PeerCount,
			Stale:         age > 2*time.Minute,
			UpdatedAgo:    formatAgo(age),
		}
	}
	if h.Cfg.Prices.Enabled {
		if p, err := h.Store.LatestPrice(ctx, h.Cfg.Prices.Pair); err == nil && p != nil {
			v.PriceAvailable = true
			v.Price = p.Price
			v.PriceTrend = p.Trend
			v.PriceLow = p.Low
			v.PriceHigh = p.High
			v.PriceQuote = h.Cfg.Prices.QuoteSymbol
			v.PriceMarketURL = h.Cfg.Prices.MarketURL
			v.PriceAgeMin = int(time.Since(time.Unix(p.TS, 0)).Minutes())
			if v.Supply > 0 && v.Price > 0 {
				v.MarketCapAvailable = true
				v.MarketCap = v.Supply * v.Price
			}
		}
	}
	return v
}

func formatAgo(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return strconv.Itoa(int(d/time.Minute)) + "m ago"
	}
	return strconv.Itoa(int(d/time.Hour)) + "h ago"
}

// ---- page handlers ----

// Blocks renders the landing page (was /, now shows latest blocks).
func (h *Handlers) Blocks(w http.ResponseWriter, r *http.Request) {
	blocks, err := h.Store.RecentBlocks(r.Context(), 50)
	if err != nil {
		h.Log.Error("blocks: recent blocks", "err", err)
	}
	h.render(w, r, "blocks.html", pageData{
		Title:       h.Cfg.UI.SiteName + " — Latest blocks",
		Active:      "blocks",
		UI:          h.Cfg.UI,
		HeaderStats: h.newHeaderStats(r.Context()),
		Body:        map[string]any{"Blocks": blocks},
	})
}

// Transactions renders /txs with pagination.
func (h *Handlers) Transactions(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r.URL.Query().Get("page"))
	per := h.Cfg.Transactions.PerPage
	if per <= 0 {
		per = 100
	}
	offset := (page - 1) * per

	total, err := h.Store.CountTxs(r.Context())
	if err != nil {
		h.Log.Error("txs: count", "err", err)
	}
	txs, err := h.Store.TxsPage(r.Context(), offset, per)
	if err != nil {
		h.Log.Error("txs: page", "err", err)
	}
	totalPages := int((total + int64(per) - 1) / int64(per))
	if totalPages < 1 {
		totalPages = 1
	}
	h.render(w, r, "txs.html", pageData{
		Title:       h.Cfg.UI.SiteName + " — Transactions",
		Active:      "txs",
		UI:          h.Cfg.UI,
		HeaderStats: h.newHeaderStats(r.Context()),
		Body: map[string]any{
			"Txs":         txs,
			"Page":        page,
			"PerPage":     per,
			"Total":       total,
			"TotalPages":  totalPages,
			"HasPrev":     page > 1,
			"HasNext":     page < totalPages,
			"PrevPage":    page - 1,
			"NextPage":    page + 1,
			"GreenBelow":  h.Cfg.Transactions.GreenBelow,
			"YellowBelow": h.Cfg.Transactions.YellowBelow,
			"RedBelow":    h.Cfg.Transactions.RedBelow,
		},
	})
}

// BlockDetail renders /blocks/{height}.
func (h *Handlers) BlockDetail(w http.ResponseWriter, r *http.Request) {
	heightStr := chi.URLParam(r, "height")
	height, err := strconv.ParseInt(heightStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid height", http.StatusBadRequest)
		return
	}
	blk, err := h.Store.BlockByHeight(r.Context(), height)
	if err != nil {
		h.Log.Error("block detail: lookup", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if blk == nil {
		http.Error(w, "block not indexed", http.StatusNotFound)
		return
	}
	txs, _ := h.Store.TxsInBlock(r.Context(), height)
	h.render(w, r, "block_detail.html", pageData{
		Title:       h.Cfg.UI.SiteName + " — Block #" + heightStr,
		Active:      "block",
		UI:          h.Cfg.UI,
		HeaderStats: h.newHeaderStats(r.Context()),
		Body: map[string]any{
			"Block":       blk,
			"Txs":         txs,
			"GreenBelow":  h.Cfg.Transactions.GreenBelow,
			"YellowBelow": h.Cfg.Transactions.YellowBelow,
			"RedBelow":    h.Cfg.Transactions.RedBelow,
		},
	})
}

// TxDetail renders /txs/{txid}.
func (h *Handlers) TxDetail(w http.ResponseWriter, r *http.Request) {
	txid := chi.URLParam(r, "txid")
	tx, outs, err := h.Store.TxByID(r.Context(), txid)
	if err != nil {
		h.Log.Error("tx detail: lookup", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if tx == nil {
		http.Error(w, "tx not indexed", http.StatusNotFound)
		return
	}
	ins, err := h.Store.InputsFor(r.Context(), txid)
	if err != nil {
		h.Log.Error("tx detail: inputs", "err", err)
	}
	inputsFromRPC := false
	// Legacy txs indexed before inputs were persisted: fall back to a live
	// RPC fetch so the user still sees the vin list.
	if len(ins) == 0 && tx.VinCount > 0 && h.RPC != nil {
		if full, rpcErr := h.RPC.GetRawTransaction(r.Context(), txid); rpcErr == nil && full != nil {
			ins = inputsFromRawTx(txid, full)
			inputsFromRPC = true
			// Try to resolve each (prev_txid, prev_vout) against our own tx_outputs.
			for i := range ins {
				if ins[i].PrevTxid == "" {
					continue
				}
				if outs2, e := h.Store.OutputsFor(r.Context(), ins[i].PrevTxid); e == nil {
					for _, o := range outs2 {
						if o.Vout == ins[i].PrevVout {
							ins[i].Address = o.Address
							ins[i].Amount = o.Amount
							ins[i].Resolved = true
							break
						}
					}
				}
			}
		} else if rpcErr != nil {
			h.Log.Warn("tx detail: rpc fallback", "err", rpcErr)
		}
	}

	// Totals for the summary line: sum of resolved input amounts (when we
	// have them), sum of outputs, and implied fee = sum(in) - sum(out).
	var totalIn float64
	inputsResolvedAll := len(ins) > 0
	for _, in := range ins {
		if in.Coinbase != "" {
			inputsResolvedAll = true // coinbase has no input value; treat as known
			continue
		}
		if !in.Resolved {
			inputsResolvedAll = false
		}
		totalIn += in.Amount
	}
	fee := 0.0
	if inputsResolvedAll && totalIn > tx.TotalOut {
		fee = totalIn - tx.TotalOut
	}

	h.render(w, r, "tx_detail.html", pageData{
		Title:       h.Cfg.UI.SiteName + " — Tx " + txid,
		Active:      "tx",
		UI:          h.Cfg.UI,
		HeaderStats: h.newHeaderStats(r.Context()),
		Body: map[string]any{
			"Tx":                tx,
			"Inputs":            ins,
			"Outputs":           outs,
			"InputsFromRPC":     inputsFromRPC,
			"InputsResolvedAll": inputsResolvedAll,
			"TotalIn":           totalIn,
			"Fee":               fee,
			"GreenBelow":        h.Cfg.Transactions.GreenBelow,
			"YellowBelow":       h.Cfg.Transactions.YellowBelow,
			"RedBelow":          h.Cfg.Transactions.RedBelow,
		},
	})
}

// inputsFromRawTx converts an RPC-fetched tx into our store.TxInput shape.
func inputsFromRawTx(txid string, t *rpc.Tx) []store.TxInput {
	out := make([]store.TxInput, 0, len(t.Vin))
	for i, v := range t.Vin {
		out = append(out, store.TxInput{
			Txid:     txid,
			Idx:      i,
			PrevTxid: v.Txid,
			PrevVout: v.Vout,
			Coinbase: v.Coinbase,
		})
	}
	return out
}

// Movements renders /movements (unchanged).
func (h *Handlers) Movements(w http.ResponseWriter, r *http.Request) {
	mvs, err := h.Store.Movements(r.Context(), h.Cfg.Movements.ThresholdLow, 50)
	if err != nil {
		h.Log.Error("movements", "err", err)
	}
	h.render(w, r, "movements.html", pageData{
		Title:       h.Cfg.UI.SiteName + " — Large transactions",
		Active:      "movements",
		UI:          h.Cfg.UI,
		HeaderStats: h.newHeaderStats(r.Context()),
		Body: map[string]any{
			"Movements":     mvs,
			"ThresholdLow":  h.Cfg.Movements.ThresholdLow,
			"ThresholdMid":  h.Cfg.Movements.ThresholdMid,
			"ThresholdHigh": h.Cfg.Movements.ThresholdHigh,
		},
	})
}

// Network renders /network — list + map views share the same data.
func (h *Handlers) Network(w http.ResponseWriter, r *http.Request) {
	hours := h.Cfg.Network.HistoryHours
	if hours <= 0 {
		hours = 24
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	peers, err := h.Store.ListPeersActiveSince(r.Context(), since)
	if err != nil {
		h.Log.Error("network", "err", err)
	}
	now := time.Now()
	liveCount := 0
	inbound := 0
	outbound := 0
	versions := map[string]int{}
	countries := map[string]int{}
	type mapPoint struct {
		Addr    string  `json:"addr"`
		Country string  `json:"country"`
		Code    string  `json:"code"`
		Lat     float64 `json:"lat"`
		Lng     float64 `json:"lng"`
		Live    bool    `json:"live"`
		Subver  string  `json:"subver"`
		Height  int64   `json:"height"`
	}
	points := make([]mapPoint, 0, len(peers))
	for _, p := range peers {
		live := store.IsPeerCurrentlyConnected(p, now)
		if live {
			liveCount++
			if p.Inbound {
				inbound++
			} else {
				outbound++
			}
		}
		v := p.Subver
		if v == "" {
			v = "unknown"
		}
		versions[v]++
		if p.CountryCode != "" {
			countries[p.CountryCode]++
		}
		if p.Latitude != 0 || p.Longitude != 0 {
			points = append(points, mapPoint{
				Addr:    p.Addr,
				Country: p.Country,
				Code:    p.CountryCode,
				Lat:     p.Latitude,
				Lng:     p.Longitude,
				Live:    live,
				Subver:  p.Subver,
				Height:  p.Height,
			})
		}
	}
	mapJSON, _ := json.Marshal(points)
	h.render(w, r, "network.html", pageData{
		Title:       h.Cfg.UI.SiteName + " — Network",
		Active:      "network",
		UI:          h.Cfg.UI,
		HeaderStats: h.newHeaderStats(r.Context()),
		Body: map[string]any{
			"Peers":         peers,
			"Inbound":       inbound,
			"Outbound":      outbound,
			"LiveCount":     liveCount,
			"Versions":      versions,
			"Countries":     countries,
			"HistoryHours":  hours,
			"Now":           now.Unix(),
			"MapPointsJSON": template.JS(mapJSON),
		},
	})
}

func (h *Handlers) render(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	tpl := h.Tpl.Page(name)
	if tpl == nil {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(w, "layout", data); err != nil {
		h.Log.Error("render", "name", name, "err", err)
	}
}

// ---- partials ----

func (h *Handlers) HeaderStats(w http.ResponseWriter, r *http.Request) {
	stats := h.newHeaderStats(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.Tpl.Part("header_stats").ExecuteTemplate(w, "header_stats", stats); err != nil {
		h.Log.Error("header stats partial", "err", err)
	}
}

func (h *Handlers) LatestBlocksPartial(w http.ResponseWriter, r *http.Request) {
	blocks, err := h.Store.RecentBlocks(r.Context(), 50)
	if err != nil {
		h.Log.Error("latest blocks partial", "err", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tpl := h.Tpl.Part("block_row")
	for _, b := range blocks {
		if err := tpl.ExecuteTemplate(w, "block_row", b); err != nil {
			h.Log.Error("block row", "err", err)
			return
		}
	}
}

// LatestTxPartial powers the first page of /txs live-refresh only.
func (h *Handlers) LatestTxPartial(w http.ResponseWriter, r *http.Request) {
	per := h.Cfg.Transactions.PerPage
	if per <= 0 {
		per = 100
	}
	txs, err := h.Store.TxsPage(r.Context(), 0, per)
	if err != nil {
		h.Log.Error("latest tx partial", "err", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tpl := h.Tpl.Part("tx_row")
	for _, t := range txs {
		if err := tpl.ExecuteTemplate(w, "tx_row", map[string]any{
			"Tx":          t,
			"GreenBelow":  h.Cfg.Transactions.GreenBelow,
			"YellowBelow": h.Cfg.Transactions.YellowBelow,
			"RedBelow":    h.Cfg.Transactions.RedBelow,
		}); err != nil {
			h.Log.Error("tx row", "err", err)
			return
		}
	}
}

func (h *Handlers) MovementsPartial(w http.ResponseWriter, r *http.Request) {
	mvs, err := h.Store.Movements(r.Context(), h.Cfg.Movements.ThresholdLow, 50)
	if err != nil {
		h.Log.Error("movements partial", "err", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tpl := h.Tpl.Part("movement_row")
	for _, m := range mvs {
		if err := tpl.ExecuteTemplate(w, "movement_row", map[string]any{
			"Tx":            m,
			"ThresholdLow":  h.Cfg.Movements.ThresholdLow,
			"ThresholdMid":  h.Cfg.Movements.ThresholdMid,
			"ThresholdHigh": h.Cfg.Movements.ThresholdHigh,
		}); err != nil {
			h.Log.Error("movement row", "err", err)
			return
		}
	}
}

func (h *Handlers) PeersPartial(w http.ResponseWriter, r *http.Request) {
	hours := h.Cfg.Network.HistoryHours
	if hours <= 0 {
		hours = 24
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	peers, err := h.Store.ListPeersActiveSince(r.Context(), since)
	if err != nil {
		h.Log.Error("peers partial", "err", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tpl := h.Tpl.Part("peer_row")
	now := time.Now().Unix()
	for _, p := range peers {
		if err := tpl.ExecuteTemplate(w, "peer_row", map[string]any{
			"Peer": p,
			"Now":  now,
		}); err != nil {
			h.Log.Error("peer row", "err", err)
			return
		}
	}
}

// PriceSeries serves a price history for the configured pair as JSON for uPlot.
// Window defaults to 24 h.
func (h *Handlers) PriceSeries(w http.ResponseWriter, r *http.Request) {
	if !h.Cfg.Prices.Enabled {
		_, _ = w.Write([]byte(`[[],[]]`))
		return
	}
	since := time.Now().Add(-24 * time.Hour).Unix()
	rows, err := h.Store.PriceSeries(r.Context(), h.Cfg.Prices.Pair, since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ts := make([]int64, 0, len(rows))
	price := make([]float64, 0, len(rows))
	for _, row := range rows {
		ts = append(ts, row.TS)
		price = append(price, row.Price)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode([]any{ts, price})
}

// HashrateSeries serves the last 24h of hashrate snapshots as JSON for uPlot.
func (h *Handlers) HashrateSeries(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-24 * time.Hour).Unix()
	rows, err := h.Store.StatsSeries(r.Context(), since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ts := make([]int64, 0, len(rows))
	hps := make([]float64, 0, len(rows))
	for _, row := range rows {
		ts = append(ts, row.TS)
		hps = append(hps, row.HashrateHPS)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode([]any{ts, hps})
}

// Search classifies the query string and 302-redirects to the right detail
// page. Unknown queries land on an address page that will simply show
// "no history" if the address is untracked.
func (h *Handlers) Search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	switch {
	case isAllDigits(q):
		http.Redirect(w, r, "/blocks/"+q, http.StatusFound)
	case len(q) == 64 && isHex(q):
		http.Redirect(w, r, "/txs/"+q, http.StatusFound)
	default:
		http.Redirect(w, r, "/address/"+url.PathEscape(q), http.StatusFound)
	}
}

// AddressDetail renders /address/{addr}.
func (h *Handlers) AddressDetail(w http.ResponseWriter, r *http.Request) {
	addr := chi.URLParam(r, "addr")
	page := parsePage(r.URL.Query().Get("page"))
	per := h.Cfg.Transactions.PerPage
	if per <= 0 {
		per = 100
	}
	offset := (page - 1) * per

	sum, err := h.Store.AddressSummary(r.Context(), addr)
	if err != nil {
		h.Log.Error("address summary", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	txs, err := h.Store.AddressTxs(r.Context(), addr, offset, per)
	if err != nil {
		h.Log.Error("address txs", "err", err)
	}
	totalPages := int((sum.TxCount + int64(per) - 1) / int64(per))
	if totalPages < 1 {
		totalPages = 1
	}
	h.render(w, r, "address_detail.html", pageData{
		Title:       h.Cfg.UI.SiteName + " — Address",
		Active:      "address",
		UI:          h.Cfg.UI,
		HeaderStats: h.newHeaderStats(r.Context()),
		Body: map[string]any{
			"Summary":     sum,
			"Txs":         txs,
			"Page":        page,
			"PerPage":     per,
			"TotalPages":  totalPages,
			"HasPrev":     page > 1,
			"HasNext":     page < totalPages,
			"PrevPage":    page - 1,
			"NextPage":    page + 1,
			"GreenBelow":  h.Cfg.Transactions.GreenBelow,
			"YellowBelow": h.Cfg.Transactions.YellowBelow,
			"RedBelow":    h.Cfg.Transactions.RedBelow,
		},
	})
}

// Robots emits a permissive robots.txt pointing at our sitemap.
func (h *Handlers) Robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	scheme := requestScheme(r)
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\nSitemap: " + scheme + "://" + r.Host + "/sitemap.xml\n"))
}

// Sitemap emits a fresh XML sitemap with static pages + last N blocks + last N txs.
func (h *Handlers) Sitemap(w http.ResponseWriter, r *http.Request) {
	scheme := requestScheme(r)
	base := scheme + "://" + r.Host
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")

	write := func(loc, lastmod, changefreq, priority string) {
		b.WriteString("  <url><loc>" + loc + "</loc>")
		if lastmod != "" {
			b.WriteString("<lastmod>" + lastmod + "</lastmod>")
		}
		if changefreq != "" {
			b.WriteString("<changefreq>" + changefreq + "</changefreq>")
		}
		if priority != "" {
			b.WriteString("<priority>" + priority + "</priority>")
		}
		b.WriteString("</url>\n")
	}

	write(base+"/", "", "always", "1.0")
	write(base+"/txs", "", "always", "0.9")
	write(base+"/network", "", "hourly", "0.5")

	const maxEntries = 500
	if blocks, err := h.Store.RecentBlocks(r.Context(), maxEntries); err == nil {
		for _, blk := range blocks {
			lm := time.Unix(blk.Time, 0).UTC().Format("2006-01-02T15:04:05Z")
			write(base+"/blocks/"+strconv.FormatInt(blk.Height, 10), lm, "never", "0.4")
		}
	}
	if txs, err := h.Store.TxsPage(r.Context(), 0, maxEntries); err == nil {
		for _, t := range txs {
			lm := time.Unix(t.Time, 0).UTC().Format("2006-01-02T15:04:05Z")
			write(base+"/txs/"+t.Txid, lm, "never", "0.3")
		}
	}
	b.WriteString(`</urlset>` + "\n")
	_, _ = w.Write([]byte(b.String()))
}

func requestScheme(r *http.Request) string {
	if r.Header.Get("X-Forwarded-Proto") == "https" || r.TLS != nil {
		return "https"
	}
	return "http"
}

// ---- helpers ----

func isAllDigits(s string) bool {
	if s == "" || len(s) > 12 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func parsePage(s string) int {
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return n
}
