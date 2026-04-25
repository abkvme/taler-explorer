package store

import (
	"context"
	"net"
	"strings"
	"time"
)

// UpsertPeers merges the current getpeerinfo snapshot into the peers table,
// bumping last_seen + refreshing dynamic fields while preserving history for
// peers not currently connected. Callers pass `nowTS` for consistency across rows.
func (s *Store) UpsertPeers(ctx context.Context, peers []Peer) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Defensive update: only overwrite a stored field when the incoming
	// value is meaningful. A peer mid-handshake (subver=='', height==-1)
	// must not erase the previously-known good data for that addr. last_seen
	// always overwrites — that's the point of the upsert.
	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO peers(addr, port, protocol, subver, inbound, height, ping_ms, conn_time, country, country_code, latitude, longitude, last_seen)
        VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(addr) DO UPDATE SET
            port=CASE WHEN excluded.port > 0 THEN excluded.port ELSE peers.port END,
            protocol=CASE WHEN excluded.protocol > 0 THEN excluded.protocol ELSE peers.protocol END,
            subver=COALESCE(NULLIF(excluded.subver, ''), peers.subver),
            inbound=excluded.inbound,
            height=CASE WHEN excluded.height > 0 THEN excluded.height ELSE peers.height END,
            ping_ms=CASE WHEN excluded.ping_ms > 0 THEN excluded.ping_ms ELSE peers.ping_ms END,
            conn_time=CASE WHEN excluded.conn_time > 0 THEN excluded.conn_time ELSE peers.conn_time END,
            country=COALESCE(NULLIF(excluded.country,''), peers.country),
            country_code=COALESCE(NULLIF(excluded.country_code,''), peers.country_code),
            latitude=COALESCE(NULLIF(excluded.latitude,0), peers.latitude),
            longitude=COALESCE(NULLIF(excluded.longitude,0), peers.longitude),
            last_seen=excluded.last_seen`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range peers {
		if _, err := stmt.ExecContext(ctx, p.Addr, p.Port, p.Protocol, p.Subver, boolToInt(p.Inbound), p.Height, p.PingMs, p.ConnTime, p.Country, p.CountryCode, p.Latitude, p.Longitude, p.LastSeen); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PrunePeersOlderThan removes peers whose last_seen < cutoff. Keeps the table bounded.
func (s *Store) PrunePeersOlderThan(ctx context.Context, cutoff int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM peers WHERE last_seen < ?`, cutoff)
	return err
}

// PruneLocalPeers removes any rows whose addr is a loopback / RFC1918 private /
// link-local / unspecified address (Docker NAT noise, LAN traffic, etc.).
// Returns the number of rows deleted. Backed by stdlib net.IP predicates so the
// rule stays in sync with the indexer-side filter.
func (s *Store) PruneLocalPeers(ctx context.Context) (int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT addr FROM peers`)
	if err != nil {
		return 0, err
	}
	var toDelete []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			rows.Close()
			return 0, err
		}
		host := strings.Trim(strings.TrimSpace(addr), "[]")
		if host == "" {
			toDelete = append(toDelete, addr)
			continue
		}
		ip := net.ParseIP(host)
		if ip == nil {
			continue // hostname or otherwise unparseable — leave alone
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			toDelete = append(toDelete, addr)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, a := range toDelete {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM peers WHERE addr = ?`, a); err != nil {
			return 0, err
		}
	}
	return len(toDelete), nil
}

// ListPeersActiveSince returns peers last seen at or after sinceTS. A peer is
// considered "currently connected" if its last_seen is within the last 2 min.
func (s *Store) ListPeersActiveSince(ctx context.Context, sinceTS int64) ([]Peer, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT addr, COALESCE(port, 0), COALESCE(protocol, 0), COALESCE(subver, ''),
               COALESCE(inbound, 0), COALESCE(height, 0), COALESCE(ping_ms, 0),
               COALESCE(conn_time, 0), COALESCE(country, ''), COALESCE(country_code, ''),
               COALESCE(latitude, 0), COALESCE(longitude, 0),
               last_seen
        FROM peers
        WHERE last_seen >= ?
        ORDER BY last_seen DESC, addr ASC`, sinceTS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Peer
	for rows.Next() {
		var p Peer
		var inbound int
		if err := rows.Scan(&p.Addr, &p.Port, &p.Protocol, &p.Subver, &inbound, &p.Height, &p.PingMs, &p.ConnTime, &p.Country, &p.CountryCode, &p.Latitude, &p.Longitude, &p.LastSeen); err != nil {
			return nil, err
		}
		p.Inbound = inbound != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

// IsPeerCurrentlyConnected is a small helper template handlers use to decide
// whether to render the 'live' dot vs 'seen' dot.
func IsPeerCurrentlyConnected(p Peer, now time.Time) bool {
	return now.Unix()-p.LastSeen <= 120
}

// CountPeersActiveSince returns just the count for summary KPIs.
func (s *Store) CountPeersActiveSince(ctx context.Context, sinceTS int64) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM peers WHERE last_seen >= ?`, sinceTS).Scan(&n)
	return n, err
}

// GeoIP cache helpers.

// GeoIPEntry mirrors the cached data shape.
type GeoIPEntry struct {
	Country     string
	CountryCode string
	Latitude    float64
	Longitude   float64
}

func (s *Store) GetGeoIP(ctx context.Context, ip string) (GeoIPEntry, bool, error) {
	var e GeoIPEntry
	err := s.DB.QueryRowContext(ctx, `
        SELECT COALESCE(country,''), COALESCE(country_code,''),
               COALESCE(latitude, 0), COALESCE(longitude, 0)
        FROM geoip_cache WHERE ip = ?`, ip).
		Scan(&e.Country, &e.CountryCode, &e.Latitude, &e.Longitude)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return e, false, nil
		}
		return e, false, err
	}
	return e, true, nil
}

func (s *Store) PutGeoIP(ctx context.Context, ip string, e GeoIPEntry, cachedAt int64) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO geoip_cache(ip, country, country_code, latitude, longitude, cached_at)
        VALUES(?, ?, ?, ?, ?, ?)
        ON CONFLICT(ip) DO UPDATE SET
            country=excluded.country,
            country_code=excluded.country_code,
            latitude=excluded.latitude,
            longitude=excluded.longitude,
            cached_at=excluded.cached_at`,
		ip, e.Country, e.CountryCode, e.Latitude, e.Longitude, cachedAt)
	return err
}
