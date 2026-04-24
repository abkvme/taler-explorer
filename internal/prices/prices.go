// Package prices polls the qutrade.io public ticker endpoint for a given
// trading pair (default tlr_usdt) and caches snapshots in SQLite.
//
// Qutrade returns numeric fields as JSON strings, so the client parses them
// through a helper that also tolerates missing/null values.
package prices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"taler-explorer/internal/store"
)

// --- client ---

type Client struct {
	endpoint string
	http     *http.Client
}

func NewClient(endpoint string) *Client {
	if endpoint == "" {
		endpoint = "https://qutrade.io/api/v1/market_data/"
	}
	return &Client{
		endpoint: endpoint,
		http:     &http.Client{Timeout: 10 * time.Second},
	}
}

// Ticker is the parsed, numeric shape of a single market entry.
type Ticker struct {
	Pair        string
	Price       float64
	Low         float64
	High        float64
	Trend       float64
	VolumeBase  float64
	VolumeQuote float64
	Timestamp   int64
}

type rawEntry struct {
	Price        strNum `json:"price"`
	Low          strNum `json:"low"`
	High         strNum `json:"high"`
	Trend        strNum `json:"trend"`
	Asset1Volume strNum `json:"asset_1_volume"`
	Asset2Volume strNum `json:"asset_2_volume"`
	Timestamp    strNum `json:"timestamp"`
}

type rawResp struct {
	Result string              `json:"result"`
	List   map[string]rawEntry `json:"list"`
	Error  string              `json:"error"`
}

// strNum tolerates both "0.00721" and 0.00721 in the JSON.
type strNum float64

func (s *strNum) UnmarshalJSON(b []byte) error {
	str := strings.TrimSpace(string(b))
	if str == "null" || str == `""` || str == "" {
		*s = 0
		return nil
	}
	str = strings.Trim(str, `"`)
	if str == "" {
		*s = 0
		return nil
	}
	f, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return err
	}
	*s = strNum(f)
	return nil
}

func (c *Client) Fetch(ctx context.Context, pair string) (*Ticker, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("pair", pair)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("qutrade http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var r rawResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("qutrade decode: %w", err)
	}
	if r.Result != "success" {
		msg := r.Error
		if msg == "" {
			msg = r.Result
		}
		return nil, fmt.Errorf("qutrade api error: %s", msg)
	}
	entry, ok := r.List[pair]
	if !ok {
		return nil, fmt.Errorf("qutrade response missing pair %q", pair)
	}
	return &Ticker{
		Pair:        pair,
		Price:       float64(entry.Price),
		Low:         float64(entry.Low),
		High:        float64(entry.High),
		Trend:       float64(entry.Trend),
		VolumeBase:  float64(entry.Asset1Volume),
		VolumeQuote: float64(entry.Asset2Volume),
		Timestamp:   int64(entry.Timestamp),
	}, nil
}

// --- worker ---

type Worker struct {
	Client   *Client
	Store    *store.Store
	Log      *slog.Logger
	Pair     string
	Interval time.Duration
}

func (w *Worker) Run(ctx context.Context) error {
	if w.Pair == "" {
		w.Pair = "tlr_usdt"
	}
	if w.Interval <= 0 {
		w.Interval = 10 * time.Minute
	}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	trim := time.NewTicker(6 * time.Hour)
	defer trim.Stop()

	w.once(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			w.once(ctx)
		case <-trim.C:
			cutoff := time.Now().Add(-30 * 24 * time.Hour).Unix()
			if err := w.Store.TrimPricesOlderThan(ctx, cutoff); err != nil {
				w.Log.Warn("trim prices", "err", err)
			}
		}
	}
}

func (w *Worker) once(ctx context.Context) {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tk, err := w.Client.Fetch(fetchCtx, w.Pair)
	if err != nil {
		w.Log.Warn("qutrade fetch", "pair", w.Pair, "err", err)
		return
	}
	ts := tk.Timestamp
	if ts <= 0 {
		ts = time.Now().Unix()
	}
	if err := w.Store.InsertPrice(ctx, store.PriceSnapshot{
		TS:          ts,
		Pair:        tk.Pair,
		Price:       tk.Price,
		Low:         tk.Low,
		High:        tk.High,
		Trend:       tk.Trend,
		VolumeBase:  tk.VolumeBase,
		VolumeQuote: tk.VolumeQuote,
	}); err != nil {
		w.Log.Warn("insert price", "err", err)
	}
}

// sentinel error preserved for future call sites that want to distinguish
// transport failures from malformed responses.
var ErrNoData = errors.New("no ticker data")
