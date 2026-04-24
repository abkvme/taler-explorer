// Package geoip resolves peer IPs to a country name, ISO code, and
// latitude/longitude via a free HTTP endpoint (reallyfreegeoip.org by default),
// cached in SQLite for 24h.
package geoip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"taler-explorer/internal/store"
)

type Lookup struct {
	store    *store.Store
	endpoint string
	http     *http.Client
	enabled  bool
}

type Result struct {
	Country     string
	CountryCode string
	Latitude    float64
	Longitude   float64
}

func New(s *store.Store, endpoint string, enabled bool) *Lookup {
	return &Lookup{
		store:    s,
		endpoint: endpoint,
		enabled:  enabled,
		http:     &http.Client{Timeout: 8 * time.Second},
	}
}

// Resolve returns a best-effort GeoIP lookup for the given `host:port`.
// Cached 24h in SQLite.
func (l *Lookup) Resolve(ctx context.Context, addr string) (Result, error) {
	if !l.enabled {
		return Result{}, nil
	}
	ip := extractIP(addr)
	if ip == "" {
		return Result{}, nil
	}

	// Cache hit — but treat (0,0) coords as "unresolved" so existing rows from
	// before the lat/lng migration get re-fetched on first access.
	if e, ok, err := l.store.GetGeoIP(ctx, ip); err == nil && ok {
		if e.Latitude != 0 || e.Longitude != 0 {
			return Result{
				Country:     e.Country,
				CountryCode: e.CountryCode,
				Latitude:    e.Latitude,
				Longitude:   e.Longitude,
			}, nil
		}
	}

	u := fmt.Sprintf(l.endpoint, url.PathEscape(ip))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Result{}, err
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Result{}, fmt.Errorf("geoip http %d", resp.StatusCode)
	}
	var body struct {
		Country     string  `json:"country_name"`
		CountryCode string  `json:"country_code"`
		Latitude    float64 `json:"latitude"`
		Longitude   float64 `json:"longitude"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Result{}, errors.Join(errors.New("geoip decode"), err)
	}
	res := Result{
		Country:     body.Country,
		CountryCode: body.CountryCode,
		Latitude:    body.Latitude,
		Longitude:   body.Longitude,
	}
	if err := l.store.PutGeoIP(ctx, ip, store.GeoIPEntry{
		Country:     res.Country,
		CountryCode: res.CountryCode,
		Latitude:    res.Latitude,
		Longitude:   res.Longitude,
	}, time.Now().Unix()); err != nil {
		return res, nil // best-effort cache
	}
	return res, nil
}

func extractIP(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return strings.Trim(addr, "[]")
}
