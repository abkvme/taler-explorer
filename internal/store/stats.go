package store

import (
	"context"
	"database/sql"
)

func (s *Store) InsertStats(ctx context.Context, snap StatsSnapshot) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO stats_snapshots(ts, supply, difficulty_pow, difficulty_pos, hashrate_hps, height, peer_count)
        VALUES(?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(ts) DO UPDATE SET
            supply=excluded.supply,
            difficulty_pow=excluded.difficulty_pow,
            difficulty_pos=excluded.difficulty_pos,
            hashrate_hps=excluded.hashrate_hps,
            height=excluded.height,
            peer_count=excluded.peer_count`,
		snap.TS, snap.Supply, snap.DifficultyPOW, snap.DifficultyPOS, snap.HashrateHPS, snap.Height, snap.PeerCount)
	return err
}

func (s *Store) LatestStats(ctx context.Context) (*StatsSnapshot, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT ts, supply, difficulty_pow, difficulty_pos, hashrate_hps, height, peer_count
        FROM stats_snapshots ORDER BY ts DESC LIMIT 1`)
	var snap StatsSnapshot
	err := row.Scan(&snap.TS, &snap.Supply, &snap.DifficultyPOW, &snap.DifficultyPOS, &snap.HashrateHPS, &snap.Height, &snap.PeerCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// StatsSeries returns snapshots within [sinceTs, now], oldest first. Used to
// feed the 24h hashrate sparkline.
func (s *Store) StatsSeries(ctx context.Context, sinceTs int64) ([]StatsSnapshot, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT ts, supply, difficulty_pow, difficulty_pos, hashrate_hps, height, peer_count
        FROM stats_snapshots
        WHERE ts >= ?
        ORDER BY ts ASC`, sinceTs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatsSnapshot
	for rows.Next() {
		var snap StatsSnapshot
		if err := rows.Scan(&snap.TS, &snap.Supply, &snap.DifficultyPOW, &snap.DifficultyPOS, &snap.HashrateHPS, &snap.Height, &snap.PeerCount); err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// TrimStatsOlderThan is called periodically; keeps the table bounded.
func (s *Store) TrimStatsOlderThan(ctx context.Context, cutoff int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM stats_snapshots WHERE ts < ?`, cutoff)
	return err
}
