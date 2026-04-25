package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"taler-explorer/internal/config"
	"taler-explorer/internal/geoip"
	"taler-explorer/internal/rpc"
	"taler-explorer/internal/store"
)

type Handlers struct {
	Store   *store.Store
	Tpl     *Templates
	Cfg     *config.Config
	Log     *slog.Logger
	RPC     *rpc.Client    // optional; used as fallback when the DB lacks tx inputs
	GeoIP   *geoip.Lookup  // optional; used to resolve self IP for the network page
	Version string         // baked in at build time via -X main.version
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

// Network renders /network — every peer seen in the rolling window.
//
// No live-status concept: any peer the node connected to in the last N hours
// is listed equally. IPs are masked at the server boundary so the unredacted
// address never reaches the browser.
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

	// Prepend ourselves to the list — we're a node too. Live RPC each
	// render; cheap (2 small calls), no DB row stored.
	if selfPeers := h.fetchSelfPeers(r.Context(), now.Unix()); len(selfPeers) > 0 {
		peers = append(selfPeers, peers...)
	}

	// Sort: highest-height (most-performant) first; not-synced (height <= 0)
	// pushed to the end of the list. Stable so ties keep their existing
	// last_seen-DESC order from the DB.
	sort.SliceStable(peers, func(i, j int) bool {
		ai, aj := peers[i].Height, peers[j].Height
		badI := ai <= 0
		badJ := aj <= 0
		if badI != badJ {
			return !badI // synced (good) sorts before not-synced (bad)
		}
		if !badI {
			return ai > aj // both synced: higher height first
		}
		return false // both not-synced: keep insertion order
	})

	// Total-peers KPI breakdown:
	//   "recent & synced"  → seen in last 90 s AND height > 0 (the healthy set)
	//   "other"            → everything else (stale, mid-handshake, no longer
	//                        connected — together in a single bucket)
	const recentWindowSec int64 = 90
	cutoffRecent := now.Unix() - recentWindowSec
	recentSyncedCount := 0
	for _, p := range peers {
		if p.Height > 0 && p.LastSeen >= cutoffRecent {
			recentSyncedCount++
		}
	}
	otherCount := len(peers) - recentSyncedCount

	// versions/countries are accumulated as maps and converted to sorted
	// slices below so the template renders in a deterministic, count-desc
	// order. "unknown" entries are intentionally dropped from the versions
	// summary — they don't carry any actionable info.
	versionsMap := map[string]int{}
	type ctyAcc struct {
		Name  string
		Count int
	}
	countriesAcc := map[string]*ctyAcc{}
	type mapPoint struct {
		Addr    string  `json:"addr"` // masked
		Country string  `json:"country"`
		Code    string  `json:"code"`
		Lat     float64 `json:"lat"`
		Lng     float64 `json:"lng"`
		Subver  string  `json:"subver"`
		Height  int64   `json:"height"`
	}
	points := make([]mapPoint, 0, len(peers))
	for _, p := range peers {
		if v := versionFamily(p.Subver); v != "" {
			versionsMap[v]++
		}
		if p.CountryCode != "" {
			c := countriesAcc[p.CountryCode]
			if c == nil {
				name := p.Country
				if name == "" {
					name = p.CountryCode
				}
				c = &ctyAcc{Name: name}
				countriesAcc[p.CountryCode] = c
			}
			c.Count++
		}
		if p.Latitude != 0 || p.Longitude != 0 {
			points = append(points, mapPoint{
				Addr:    maskIPPlain(p.Addr),
				Country: p.Country,
				Code:    p.CountryCode,
				Lat:     p.Latitude,
				Lng:     p.Longitude,
				Subver:  p.Subver,
				Height:  p.Height,
			})
		}
	}
	mapJSON, _ := json.Marshal(points)

	// Sort countries by count desc, then by name asc for stable display.
	type CountryStat struct {
		Code  string
		Name  string
		Count int
	}
	countries := make([]CountryStat, 0, len(countriesAcc))
	for code, c := range countriesAcc {
		countries = append(countries, CountryStat{Code: code, Name: c.Name, Count: c.Count})
	}
	sort.Slice(countries, func(i, j int) bool {
		if countries[i].Count != countries[j].Count {
			return countries[i].Count > countries[j].Count
		}
		return countries[i].Name < countries[j].Name
	})

	// Sort versions by count desc, then by family asc.
	type VersionStat struct {
		Family string
		Count  int
	}
	versions := make([]VersionStat, 0, len(versionsMap))
	for k, v := range versionsMap {
		versions = append(versions, VersionStat{Family: k, Count: v})
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].Count != versions[j].Count {
			return versions[i].Count > versions[j].Count
		}
		return versions[i].Family < versions[j].Family
	})

	h.render(w, r, "network.html", pageData{
		Title:       h.Cfg.UI.SiteName + " — Network",
		Active:      "network",
		UI:          h.Cfg.UI,
		HeaderStats: h.newHeaderStats(r.Context()),
		Body: map[string]any{
			"Peers":             peers,
			"Versions":          versions,
			"Countries":         countries,
			"RecentSyncedCount": recentSyncedCount,
			"OtherCount":        otherCount,
			"HistoryHours":      hours,
			"Now":               now.Unix(),
			"MapPointsJSON":     template.JS(mapJSON),
		},
	})
}

// fetchSelfPeers returns synthetic Peer entries for each public address the
// node thinks it has. Pure in-memory; never touches SQLite. Resolved through
// the same GeoIP cache as other peers, so country shows up consistently.
func (h *Handlers) fetchSelfPeers(ctx context.Context, nowTS int64) []store.Peer {
	if h.RPC == nil {
		return nil
	}
	ni, err := h.RPC.GetNetworkInfo(ctx)
	if err != nil || ni == nil {
		return nil
	}
	// localaddresses isn't on our typed NetworkInfo — pull via raw call.
	type localAddr struct {
		Address string `json:"address"`
		Port    int    `json:"port"`
	}
	type netInfoExtra struct {
		LocalAddresses []localAddr `json:"localaddresses"`
		Subversion     string      `json:"subversion"`
	}
	raw, err := h.RPC.Raw(ctx, "getnetworkinfo")
	if err != nil {
		return nil
	}
	var extra netInfoExtra
	if err := json.Unmarshal(raw, &extra); err != nil {
		return nil
	}
	if len(extra.LocalAddresses) == 0 {
		return nil
	}
	height, _ := h.RPC.GetBlockCount(ctx)

	out := make([]store.Peer, 0, len(extra.LocalAddresses))
	for _, la := range extra.LocalAddresses {
		if la.Address == "" {
			continue
		}
		country, code := "", ""
		var lat, lng float64
		if h.GeoIP != nil {
			if g, err := h.GeoIP.Resolve(ctx, la.Address); err == nil {
				country = g.Country
				code = strings.ToUpper(g.CountryCode)
				lat = g.Latitude
				lng = g.Longitude
			}
		}
		out = append(out, store.Peer{
			Addr:        la.Address,
			Port:        la.Port,
			Subver:      extra.Subversion,
			Height:      height,
			Country:     country,
			CountryCode: code,
			Latitude:    lat,
			Longitude:   lng,
			LastSeen:    nowTS,
		})
	}
	return out
}

// versionFamily collapses a peer subversion to its first two version
// components, e.g. "/Taler:0.16.3.4/" -> "0.16.x". Returns "" for empty input.
func versionFamily(subver string) string {
	v := strings.Trim(subver, "/ ")
	if v == "" {
		return ""
	}
	if i := strings.LastIndex(v, ":"); i >= 0 {
		v = v[i+1:]
	}
	v = strings.Trim(v, "/ ")
	if v == "" {
		return ""
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1] + ".x"
	}
	return v
}

// maskIPPlain returns the same masked form as the maskIP template func, but
// as a plain string suitable for embedding in JSON sent to the map JS.
func maskIPPlain(addr string) string {
	host := strings.TrimSpace(addr)
	host = strings.Trim(host, "[]")
	if host == "" {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return fmt.Sprintf("xxx.xxx.%d.%d", v4[2], v4[3])
	}
	parts := strings.Split(ip.To16().String(), ":")
	last := parts[len(parts)-1]
	if last == "" && len(parts) >= 2 {
		last = parts[len(parts)-2]
	}
	return "xxxx:…:" + last
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
// Window is 7 days — Taler trades are infrequent, so a longer window shows a
// more meaningful trend than 24h would.
func (h *Handlers) PriceSeries(w http.ResponseWriter, r *http.Request) {
	if !h.Cfg.Prices.Enabled {
		_, _ = w.Write([]byte(`[[],[]]`))
		return
	}
	since := time.Now().Add(-7 * 24 * time.Hour).Unix()
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
