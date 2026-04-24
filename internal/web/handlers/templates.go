package handlers

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"strings"
	"time"

	"taler-explorer/internal/config"
)

type Templates struct {
	UI      config.UIConfig
	Version string
	pages   map[string]*template.Template
	parts   map[string]*template.Template
}

func LoadTemplates(efs embed.FS, ui config.UIConfig, version string) (*Templates, error) {
	t := &Templates{
		UI:      ui,
		Version: version,
		pages:   map[string]*template.Template{},
		parts:   map[string]*template.Template{},
	}
	funcs := t.funcMap()

	// Read templates sub-FS so paths we use below are relative.
	tplFS, err := fs.Sub(efs, "templates")
	if err != nil {
		return nil, err
	}

	partialPaths := []string{
		"partials/header_stats.html",
		"partials/tx_row.html",
		"partials/movement_row.html",
		"partials/peer_row.html",
		"partials/block_row.html",
		"partials/pagination.html",
	}

	pages := []string{"blocks.html", "txs.html", "block_detail.html", "tx_detail.html", "address_detail.html", "movements.html", "network.html"}
	for _, p := range pages {
		files := append([]string{"layout.html", p}, partialPaths...)
		tpl, err := template.New(p).Funcs(funcs).ParseFS(tplFS, files...)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		t.pages[p] = tpl
	}

	// Partials parsed individually for direct rendering. Each partial uses
	// {{define "name"}}...{{end}} so we invoke via ExecuteTemplate(w, name, …).
	partials := []string{"header_stats", "tx_row", "movement_row", "peer_row", "block_row", "pagination"}
	for _, name := range partials {
		tpl, err := template.New(name).Funcs(funcs).ParseFS(tplFS, "partials/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse partial %s: %w", name, err)
		}
		t.parts[name] = tpl
	}

	return t, nil
}

func (t *Templates) Page(name string) *template.Template { return t.pages[name] }
func (t *Templates) Part(name string) *template.Template { return t.parts[name] }

func (t *Templates) funcMap() template.FuncMap {
	return template.FuncMap{
		"ui":          func() config.UIConfig { return t.UI },
		"fmtTLR":      fmtTLR,
		"fmtInt":      fmtInt,
		"fmtHashrate": fmtHashrate,
		"fmtDiff":     fmtDiff,
		"fmtSize":     fmtSize,
		"fmtDuration": fmtDuration,
		"truncMiddle": truncMiddle,
		"unixAgo":     unixAgo,
		"unixTime":    unixTime,
		"tierClass":   tierClass,
		"countryFlag": countryFlag,
		"default":     defaultVal,
		"dict":        dict,
		"deref":       derefInt64,
		"sub":         func(a, b int64) int64 { return a - b },
		"sub64":       func(a, b float64) float64 { return a - b },
		"txTier":       txTier,
		"pageRange":    pageRange,
		"liveSince":    liveSince,
		"cleanVersion": cleanVersion,
		"fmtTLRExact":  fmtTLRExact,
		"localTime":    localTime,
		"fmtPrice":     fmtPrice,
		"fmtTrend":     fmtTrend,
		"fmtHours":     fmtHours,
		"buildVersion": func() string { return t.Version },
	}
}

// fmtHours renders a whole-hour duration as the largest sensible unit for labels.
//   23  -> "23h"
//   24  -> "24h"
//   168 -> "7d"
//   720 -> "30d"
func fmtHours(h int) string {
	if h <= 0 {
		return "0h"
	}
	if h%24 == 0 {
		return fmt.Sprintf("%dd", h/24)
	}
	return fmt.Sprintf("%dh", h)
}

// fmtPrice renders a market price with precision adapted to its magnitude.
// Crypto pairs often have prices below 1, which %.2f would collapse to "0.00".
func fmtPrice(v float64) string {
	switch {
	case v >= 1000:
		return fmt.Sprintf("%.2f", v)
	case v >= 1:
		return fmt.Sprintf("%.4f", v)
	case v >= 0.001:
		return fmt.Sprintf("%.6f", v)
	case v > 0:
		return fmt.Sprintf("%.8f", v)
	default:
		return "—"
	}
}

// fmtTrend renders a signed percentage like "+2.14%" / "-3.13%".
func fmtTrend(v float64) string {
	if v > 0 {
		return fmt.Sprintf("+%.2f%%", v)
	}
	return fmt.Sprintf("%.2f%%", v)
}

// localTime emits a <time datetime="ISO-8601-UTC"> element whose text content
// is the UTC timestamp as a fallback. A small script in app.js rewrites the
// text to the browser's local timezone on load + after every HTMX swap.
func localTime(ts int64) template.HTML {
	if ts == 0 {
		return ""
	}
	t := time.Unix(ts, 0).UTC()
	iso := t.Format("2006-01-02T15:04:05Z")
	fallback := t.Format("2006-01-02 15:04:05")
	return template.HTML(fmt.Sprintf(
		`<time datetime="%s" data-local="auto" title="%s UTC">%s</time>`,
		iso, fallback, fallback,
	))
}

// fmtTLRExact renders a TLR amount with thousand-separators and exactly two
// decimals, returning HTML that wraps the decimal portion in a `.tlr-dec` span
// so CSS can render decimals in a smaller font. Safe HTML — the only escapable
// content is the numeric string we build ourselves.
func fmtTLRExact(v float64) template.HTML {
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	// Round to 2 decimals up-front, then split, so "1.995" -> "2.00" not "1.100".
	scaled := math.Round(v * 100)
	intPart := int64(scaled / 100)
	fracPart := int64(scaled) - intPart*100
	return template.HTML(fmt.Sprintf(
		`%s%s<span class="tlr-dec">.%02d</span>`,
		sign, fmtInt(intPart), fracPart,
	))
}

// cleanVersion extracts the numeric version from a bitcoin-style subver like
//   /Taler:0.19.6.8/     -> "0.19.6.8"
//   /TalerCore:0.16.3.4/ -> "0.16.3.4"
// Returns the input unchanged if it doesn't look like the expected shape.
func cleanVersion(s string) string {
	s = strings.Trim(s, "/ ")
	if s == "" {
		return ""
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return strings.Trim(s[i+1:], "/ ")
	}
	return s
}

// txTier maps a tx's total_out to a CSS class.
// total_out < greenBelow  -> amount-blue
// total_out < yellowBelow -> amount-green
// total_out < redBelow    -> amount-yellow
// total_out >= redBelow   -> amount-red
func txTier(total, greenBelow, yellowBelow, redBelow float64) string {
	switch {
	case total < greenBelow:
		return "amount-blue"
	case total < yellowBelow:
		return "amount-green"
	case total < redBelow:
		return "amount-yellow"
	default:
		return "amount-red"
	}
}

// pageRange returns a windowed list of page numbers centered on current page.
// Used by the pagination partial.
func pageRange(current, total, window int) []int {
	if total <= 0 {
		return []int{1}
	}
	if window < 1 {
		window = 2
	}
	start := current - window
	end := current + window
	if start < 1 {
		start = 1
	}
	if end > total {
		end = total
	}
	out := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, i)
	}
	return out
}

// liveSince compares last_seen against now; <= 120 s means "live", else "X min/h/d ago".
func liveSince(lastSeen, now int64) string {
	diff := now - lastSeen
	if diff < 0 {
		diff = 0
	}
	if diff <= 120 {
		return "live"
	}
	if diff < 3600 {
		return fmt.Sprintf("%dm ago", diff/60)
	}
	if diff < 86400 {
		return fmt.Sprintf("%dh ago", diff/3600)
	}
	return fmt.Sprintf("%dd ago", diff/86400)
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// dict builds a map[string]any from alternating key/value args, for passing
// multiple values into a sub-template.
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of args")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		k, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: non-string key at %d", i)
		}
		m[k] = pairs[i+1]
	}
	return m, nil
}

// defaultVal is pipe-friendly: `{{ .X | default "fallback" }}` maps to
// defaultVal("fallback", .X). First arg = fallback, second = value tested.
func defaultVal(def, v string) string {
	if v == "" {
		return def
	}
	return v
}

func fmtTLR(v float64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.2fM", v/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.2fK", v/1_000)
	default:
		return fmt.Sprintf("%.4f", v)
	}
}

func fmtInt(v int64) string {
	s := fmt.Sprintf("%d", v)
	// Insert thousands separators.
	n := len(s)
	if n <= 3 {
		return s
	}
	var b strings.Builder
	offset := n % 3
	if offset > 0 {
		b.WriteString(s[:offset])
		if n > offset {
			b.WriteByte(',')
		}
	}
	for i := offset; i < n; i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < n {
			b.WriteByte(',')
		}
	}
	return b.String()
}

func fmtHashrate(hps float64) string {
	switch {
	case hps >= 1e12:
		return fmt.Sprintf("%.2f TH/s", hps/1e12)
	case hps >= 1e9:
		return fmt.Sprintf("%.2f GH/s", hps/1e9)
	case hps >= 1e6:
		return fmt.Sprintf("%.2f MH/s", hps/1e6)
	case hps >= 1e3:
		return fmt.Sprintf("%.2f KH/s", hps/1e3)
	default:
		return fmt.Sprintf("%.0f H/s", hps)
	}
}

// fmtDiff uses adaptive precision. Taler PoS difficulty is often ~0.00184,
// which a flat "%.2f" collapses to "0.00"; small values need more decimals.
func fmtDiff(v float64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.3fG", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.3fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.3fK", v/1e3)
	case v >= 1:
		return fmt.Sprintf("%.4f", v)
	case v >= 0.001:
		return fmt.Sprintf("%.6f", v)
	case v > 0:
		return fmt.Sprintf("%.8f", v)
	default:
		return "0"
	}
}

func fmtSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}

func fmtDuration(seconds int64) string {
	d := time.Duration(seconds) * time.Second
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}

func unixTime(ts int64) string {
	return time.Unix(ts, 0).UTC().Format("2006-01-02 15:04:05")
}

func unixAgo(ts int64) string {
	if ts == 0 {
		return ""
	}
	d := time.Since(time.Unix(ts, 0))
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

func truncMiddle(s string, keep int) string {
	if len(s) <= keep*2+3 {
		return s
	}
	return s[:keep] + "…" + s[len(s)-keep:]
}

// tierClass picks a CSS class for the largest output of a movement tx so
// templates don't need to know about the thresholds.
// high (>= hi) => tier-high, mid (>= md) => tier-mid, else tier-low.
func tierClass(maxOut, lo, md, hi float64) string {
	if maxOut >= hi {
		return "tier-high"
	}
	if maxOut >= md {
		return "tier-mid"
	}
	return "tier-low"
}

// countryFlag returns the regional-indicator emoji sequence for a 2-letter
// ISO code (e.g. "US" -> 🇺🇸). Browsers without flag fonts show 'US'.
func countryFlag(code string) string {
	if len(code) != 2 {
		return ""
	}
	code = strings.ToUpper(code)
	// Each letter -> regional indicator symbol (0x1F1E6 + letter index).
	rs := []rune(code)
	out := []rune{}
	for _, r := range rs {
		if r < 'A' || r > 'Z' {
			return ""
		}
		out = append(out, 0x1F1E6+(r-'A'))
	}
	return string(out)
}
