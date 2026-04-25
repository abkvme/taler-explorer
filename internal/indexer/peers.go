package indexer

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"taler-explorer/internal/geoip"
	"taler-explorer/internal/rpc"
	"taler-explorer/internal/store"
)

type Peers struct {
	RPC      *rpc.Client
	Store    *store.Store
	Log      *slog.Logger
	Interval time.Duration
	GeoIP    *geoip.Lookup
	// Peers whose last_seen is older than RetainHours are pruned.
	RetainHours int

	firstRun bool
}

func (p *Peers) Run(ctx context.Context) error {
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	p.once(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			p.once(ctx)
		}
	}
}

func (p *Peers) once(ctx context.Context) {
	rawPeers, err := p.RPC.GetPeerInfo(ctx)
	if err != nil {
		p.Log.Warn("getpeerinfo failed", "err", err)
		return
	}
	now := time.Now().Unix()
	peers := make([]store.Peer, 0, len(rawPeers))
	for _, pr := range rawPeers {
		host, port := splitAddr(pr.Addr)
		// Drop loopback / RFC1918 / link-local / IPv6 ULA peers — these are
		// either Docker NAT plumbing or local-LAN noise, not real internet
		// peers, and would clutter the public network page.
		if isLocalAddress(host) {
			continue
		}
		var lat, lng float64
		country, code := "", ""
		if p.GeoIP != nil {
			if g, err := p.GeoIP.Resolve(ctx, pr.Addr); err == nil {
				country = g.Country
				code = g.CountryCode
				lat = g.Latitude
				lng = g.Longitude
			}
		}
		peers = append(peers, store.Peer{
			Addr:        host,
			Port:        port,
			Protocol:    pr.Version,
			Subver:      pr.Subver,
			Inbound:     pr.Inbound,
			Height:      pr.SyncedBlocks,
			PingMs:      pr.PingTime * 1000,
			ConnTime:    pr.ConnTime,
			Country:     country,
			CountryCode: strings.ToUpper(code),
			Latitude:    lat,
			Longitude:   lng,
			LastSeen:    now,
		})
	}
	if err := p.Store.UpsertPeers(ctx, peers); err != nil {
		p.Log.Warn("upsert peers", "err", err)
	}
	retain := p.RetainHours
	if retain <= 0 {
		retain = 24
	}
	cutoff := time.Now().Add(-time.Duration(retain) * time.Hour).Unix()
	if err := p.Store.PrunePeersOlderThan(ctx, cutoff); err != nil {
		p.Log.Warn("prune peers", "err", err)
	}
	// Observability — one structured line per poll so an operator can grep
	//   docker compose logs taler-explorer | grep "peers poll"
	// and see whether the indexer is doing its job and whether total_in_db
	// grows over time as new peers connect.
	if total, err := p.Store.CountPeersActiveSince(ctx, 0); err == nil {
		p.Log.Info("peers poll",
			"from_node", len(rawPeers),
			"after_filter", len(peers),
			"total_in_db", total,
		)
	}
	// One-time scrub of any local-range rows persisted by a pre-fix run.
	if !p.firstRun {
		p.firstRun = true
		if n, err := p.Store.PruneLocalPeers(ctx); err != nil {
			p.Log.Warn("prune local peers", "err", err)
		} else if n > 0 {
			p.Log.Info("pruned local-range peers from previous runs", "count", n)
		}
	}
}

// isLocalAddress returns true for any address that should never appear on a
// public-facing peer list: empty, loopback, RFC1918 private (incl. IPv6 ULA),
// or link-local. Backed by stdlib net.IP predicates.
func isLocalAddress(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Not parseable as IP (hostname?). Be permissive — let it through.
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

func splitAddr(addr string) (string, int) {
	if addr == "" {
		return "", 0
	}
	if i := strings.LastIndex(addr, ":"); i > 0 {
		host := addr[:i]
		host = strings.Trim(host, "[]")
		portStr := addr[i+1:]
		if port, err := strconv.Atoi(portStr); err == nil {
			return host, port
		}
	}
	return addr, 0
}
