package store

import (
	"context"
	"database/sql"
)

func (s *Store) UpsertBlock(ctx context.Context, tx *sql.Tx, b *Block) error {
	_, err := tx.ExecContext(ctx, `
        INSERT INTO blocks(height, hash, prev_hash, time, tx_count, size, bits, difficulty, is_pos, staker_address, miner_address)
        VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(height) DO UPDATE SET
            hash=excluded.hash,
            prev_hash=excluded.prev_hash,
            time=excluded.time,
            tx_count=excluded.tx_count,
            size=excluded.size,
            bits=excluded.bits,
            difficulty=excluded.difficulty,
            is_pos=excluded.is_pos,
            staker_address=excluded.staker_address,
            miner_address=excluded.miner_address
    `, b.Height, b.Hash, b.PrevHash, b.Time, b.TxCount, b.Size, b.Bits, b.Difficulty, boolToInt(b.IsPoS), nullIfEmpty(b.StakerAddress), nullIfEmpty(b.MinerAddress))
	return err
}

func (s *Store) DeleteBlocksFrom(ctx context.Context, tx *sql.Tx, height int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM tx_outputs WHERE txid IN (SELECT txid FROM txs WHERE block_height >= ?)`, height); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM txs WHERE block_height >= ?`, height); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM blocks WHERE height >= ?`, height)
	return err
}

func (s *Store) LatestHeight(ctx context.Context) (int64, error) {
	var h sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT MAX(height) FROM blocks`).Scan(&h)
	if err != nil {
		return 0, err
	}
	if !h.Valid {
		return -1, nil
	}
	return h.Int64, nil
}

func (s *Store) BlockHashAt(ctx context.Context, height int64) (string, error) {
	var h string
	err := s.DB.QueryRowContext(ctx, `SELECT hash FROM blocks WHERE height = ?`, height).Scan(&h)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return h, err
}

// BlockByHeight returns a single block. Returns nil, nil if not found.
func (s *Store) BlockByHeight(ctx context.Context, height int64) (*Block, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT height, hash, prev_hash, time, tx_count, size, bits, difficulty, is_pos,
               COALESCE(staker_address, ''), COALESCE(miner_address, '')
        FROM blocks WHERE height = ?`, height)
	var b Block
	var isPos int
	err := row.Scan(&b.Height, &b.Hash, &b.PrevHash, &b.Time, &b.TxCount, &b.Size, &b.Bits, &b.Difficulty, &isPos, &b.StakerAddress, &b.MinerAddress)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.IsPoS = isPos != 0
	return &b, nil
}

func (s *Store) RecentBlocks(ctx context.Context, limit int) ([]Block, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT height, hash, prev_hash, time, tx_count, size, bits, difficulty, is_pos,
               COALESCE(staker_address, ''), COALESCE(miner_address, '')
        FROM blocks ORDER BY height DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Block
	for rows.Next() {
		var b Block
		var isPos int
		if err := rows.Scan(&b.Height, &b.Hash, &b.PrevHash, &b.Time, &b.TxCount, &b.Size, &b.Bits, &b.Difficulty, &isPos, &b.StakerAddress, &b.MinerAddress); err != nil {
			return nil, err
		}
		b.IsPoS = isPos != 0
		out = append(out, b)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
