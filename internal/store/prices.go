package store

import (
	"context"
	"database/sql"
)

type PriceSnapshot struct {
	TS          int64
	Pair        string
	Price       float64
	Low         float64
	High        float64
	Trend       float64
	VolumeBase  float64
	VolumeQuote float64
}

func (s *Store) InsertPrice(ctx context.Context, snap PriceSnapshot) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO price_snapshots(ts, pair, price, low, high, trend, volume_base, volume_quote)
        VALUES(?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(pair, ts) DO UPDATE SET
            price=excluded.price,
            low=excluded.low,
            high=excluded.high,
            trend=excluded.trend,
            volume_base=excluded.volume_base,
            volume_quote=excluded.volume_quote`,
		snap.TS, snap.Pair, snap.Price, snap.Low, snap.High, snap.Trend, snap.VolumeBase, snap.VolumeQuote)
	return err
}

// LatestPrice returns the most recent snapshot for a pair, or nil if none yet.
func (s *Store) LatestPrice(ctx context.Context, pair string) (*PriceSnapshot, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT ts, pair, price, COALESCE(low,0), COALESCE(high,0), COALESCE(trend,0), COALESCE(volume_base,0), COALESCE(volume_quote,0)
        FROM price_snapshots WHERE pair = ? ORDER BY ts DESC LIMIT 1`, pair)
	var p PriceSnapshot
	err := row.Scan(&p.TS, &p.Pair, &p.Price, &p.Low, &p.High, &p.Trend, &p.VolumeBase, &p.VolumeQuote)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// TrimPricesOlderThan keeps the table bounded.
func (s *Store) TrimPricesOlderThan(ctx context.Context, cutoff int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM price_snapshots WHERE ts < ?`, cutoff)
	return err
}

// PriceSeries returns snapshots for a pair within [sinceTs, now], oldest first.
func (s *Store) PriceSeries(ctx context.Context, pair string, sinceTs int64) ([]PriceSnapshot, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT ts, pair, price, COALESCE(low,0), COALESCE(high,0), COALESCE(trend,0), COALESCE(volume_base,0), COALESCE(volume_quote,0)
        FROM price_snapshots
        WHERE pair = ? AND ts >= ?
        ORDER BY ts ASC`, pair, sinceTs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PriceSnapshot
	for rows.Next() {
		var p PriceSnapshot
		if err := rows.Scan(&p.TS, &p.Pair, &p.Price, &p.Low, &p.High, &p.Trend, &p.VolumeBase, &p.VolumeQuote); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
