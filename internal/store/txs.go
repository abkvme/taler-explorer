package store

import (
	"context"
	"database/sql"
)

func (s *Store) UpsertTx(ctx context.Context, tx *sql.Tx, t *Tx, inputs []TxInput, outputs []TxOutput) error {
	_, err := tx.ExecContext(ctx, `
        INSERT INTO txs(txid, block_height, idx_in_block, time, total_out, fee, is_coinbase, is_coinstake, vin_count, vout_count, max_vout)
        VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(txid) DO UPDATE SET
            block_height=excluded.block_height,
            idx_in_block=excluded.idx_in_block,
            time=excluded.time,
            total_out=excluded.total_out,
            fee=excluded.fee,
            is_coinbase=excluded.is_coinbase,
            is_coinstake=excluded.is_coinstake,
            vin_count=excluded.vin_count,
            vout_count=excluded.vout_count,
            max_vout=excluded.max_vout
    `,
		t.Txid, nullableInt(t.BlockHeight), t.IdxInBlock, t.Time, t.TotalOut, t.Fee,
		boolToInt(t.IsCoinbase), boolToInt(t.IsCoinstake), t.VinCount, t.VoutCount, t.MaxVout)
	if err != nil {
		return err
	}
	// Replace inputs + outputs atomically within the same db tx.
	if _, err := tx.ExecContext(ctx, `DELETE FROM tx_inputs WHERE txid = ?`, t.Txid); err != nil {
		return err
	}
	for _, in := range inputs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tx_inputs(txid, idx, prev_txid, prev_vout, coinbase) VALUES(?, ?, ?, ?, ?)`,
			in.Txid, in.Idx, nullIfEmpty(in.PrevTxid), nullableIntVal(in.PrevTxid != "", in.PrevVout), nullIfEmpty(in.Coinbase)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tx_outputs WHERE txid = ?`, t.Txid); err != nil {
		return err
	}
	for _, o := range outputs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tx_outputs(txid, vout, address, amount) VALUES(?, ?, ?, ?)`,
			o.Txid, o.Vout, nullIfEmpty(o.Address), o.Amount); err != nil {
			return err
		}
	}
	return nil
}

func nullableIntVal(valid bool, v int) any {
	if !valid {
		return nil
	}
	return v
}

// PruneMempool removes mempool entries (block_height IS NULL) older than cutoff
// and any txids no longer in `keep`.
func (s *Store) PruneMempool(ctx context.Context, keep map[string]bool, cutoff int64) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT txid FROM txs WHERE block_height IS NULL`)
	if err != nil {
		return err
	}
	var toDel []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !keep[id] {
			toDel = append(toDel, id)
		}
	}
	rows.Close()
	for _, id := range toDel {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM tx_outputs WHERE txid = ?`, id); err != nil {
			return err
		}
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM txs WHERE txid = ? AND block_height IS NULL`, id); err != nil {
			return err
		}
	}
	if cutoff > 0 {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM txs WHERE block_height IS NULL AND time < ?`, cutoff); err != nil {
			return err
		}
	}
	return nil
}

// CountTxs returns the total stored tx count for pagination.
func (s *Store) CountTxs(ctx context.Context) (int64, error) {
	var n int64
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM txs`).Scan(&n)
	return n, err
}

// TxsPage returns a paginated slice of txs ordered newest-first (mempool first).
func (s *Store) TxsPage(ctx context.Context, offset, limit int) ([]Tx, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT txid, block_height, idx_in_block, time, total_out, fee,
               is_coinbase, is_coinstake, vin_count, vout_count, max_vout
        FROM txs
        ORDER BY
            CASE WHEN block_height IS NULL THEN 0 ELSE 1 END,
            time DESC,
            txid DESC
        LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTxRows(rows)
}

// TxByID returns a single tx + its outputs. Returns nil, nil, nil if not found.
func (s *Store) TxByID(ctx context.Context, txid string) (*Tx, []TxOutput, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT txid, block_height, idx_in_block, time, total_out, fee,
               is_coinbase, is_coinstake, vin_count, vout_count, max_vout
        FROM txs WHERE txid = ?`, txid)
	var t Tx
	var bh sql.NullInt64
	var isCb, isCs int
	err := row.Scan(&t.Txid, &bh, &t.IdxInBlock, &t.Time, &t.TotalOut, &t.Fee, &isCb, &isCs, &t.VinCount, &t.VoutCount, &t.MaxVout)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if bh.Valid {
		v := bh.Int64
		t.BlockHeight = &v
	}
	t.IsCoinbase = isCb != 0
	t.IsCoinstake = isCs != 0

	outs, err := s.OutputsFor(ctx, txid)
	if err != nil {
		return &t, nil, err
	}
	return &t, outs, nil
}

// InputsFor returns all inputs of a tx, left-joined against tx_outputs so the
// spender's address and amount are resolved when we have indexed the source tx.
func (s *Store) InputsFor(ctx context.Context, txid string) ([]TxInput, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT i.idx,
               COALESCE(i.prev_txid, ''),
               COALESCE(i.prev_vout, -1),
               COALESCE(i.coinbase, ''),
               COALESCE(o.address, ''),
               COALESCE(o.amount, 0)
        FROM tx_inputs i
        LEFT JOIN tx_outputs o
               ON o.txid = i.prev_txid AND o.vout = i.prev_vout
        WHERE i.txid = ?
        ORDER BY i.idx ASC`, txid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TxInput
	for rows.Next() {
		var in TxInput
		in.Txid = txid
		if err := rows.Scan(&in.Idx, &in.PrevTxid, &in.PrevVout, &in.Coinbase, &in.Address, &in.Amount); err != nil {
			return nil, err
		}
		if in.PrevVout == -1 {
			in.PrevVout = 0
		}
		in.Resolved = in.Address != "" || in.Amount > 0
		out = append(out, in)
	}
	return out, rows.Err()
}

// AddressSummary returns aggregate received/sent/balance for an address.
type AddressSummary struct {
	Address        string
	Received       float64 // sum of tx_outputs.amount where address = ?
	Sent           float64 // sum of amounts of inputs whose prev output was this address
	TxCount        int64   // distinct tx count involving the address
	ReceivedCount  int64   // tx_outputs hits
	PartialHistory bool    // true when some inputs couldn't be resolved
}

func (s *Store) AddressSummary(ctx context.Context, addr string) (*AddressSummary, error) {
	res := &AddressSummary{Address: addr}

	if err := s.DB.QueryRowContext(ctx, `
        SELECT COALESCE(SUM(amount), 0), COUNT(*) FROM tx_outputs WHERE address = ?`, addr).
		Scan(&res.Received, &res.ReceivedCount); err != nil {
		return nil, err
	}

	// Sent = sum of prev outputs (with address=?) that are referenced by any
	// input we've indexed.
	if err := s.DB.QueryRowContext(ctx, `
        SELECT COALESCE(SUM(o.amount), 0)
        FROM tx_inputs i
        JOIN tx_outputs o ON o.txid = i.prev_txid AND o.vout = i.prev_vout
        WHERE o.address = ?`, addr).Scan(&res.Sent); err != nil {
		return nil, err
	}

	if err := s.DB.QueryRowContext(ctx, `
        SELECT COUNT(*) FROM (
            SELECT txid FROM tx_outputs WHERE address = ?
            UNION
            SELECT i.txid FROM tx_inputs i
              JOIN tx_outputs o ON o.txid = i.prev_txid AND o.vout = i.prev_vout
              WHERE o.address = ?
        )`, addr, addr).Scan(&res.TxCount); err != nil {
		return nil, err
	}
	return res, nil
}

// AddressTxs returns paginated txs that either paid to the address (as an
// output) or spent a UTXO owned by the address (as an input, resolved via
// tx_inputs -> tx_outputs). Newest first.
func (s *Store) AddressTxs(ctx context.Context, addr string, offset, limit int) ([]Tx, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT t.txid, t.block_height, t.idx_in_block, t.time, t.total_out, t.fee,
               t.is_coinbase, t.is_coinstake, t.vin_count, t.vout_count, t.max_vout
        FROM txs t
        WHERE t.txid IN (
            SELECT txid FROM tx_outputs WHERE address = ?
            UNION
            SELECT i.txid FROM tx_inputs i
              JOIN tx_outputs o ON o.txid = i.prev_txid AND o.vout = i.prev_vout
              WHERE o.address = ?
        )
        ORDER BY t.time DESC, t.txid DESC
        LIMIT ? OFFSET ?`, addr, addr, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTxRows(rows)
}

// OutputsFor returns all outputs of a tx, ordered by vout index.
func (s *Store) OutputsFor(ctx context.Context, txid string) ([]TxOutput, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT txid, vout, COALESCE(address,'') AS address, amount
        FROM tx_outputs WHERE txid = ? ORDER BY vout ASC`, txid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TxOutput
	for rows.Next() {
		var o TxOutput
		if err := rows.Scan(&o.Txid, &o.Vout, &o.Address, &o.Amount); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// TxsInBlock returns every tx belonging to the given block, ordered by idx.
func (s *Store) TxsInBlock(ctx context.Context, height int64) ([]Tx, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT txid, block_height, idx_in_block, time, total_out, fee,
               is_coinbase, is_coinstake, vin_count, vout_count, max_vout
        FROM txs WHERE block_height = ? ORDER BY idx_in_block ASC`, height)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTxRows(rows)
}

func (s *Store) RecentTxs(ctx context.Context, limit int) ([]Tx, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT txid, block_height, idx_in_block, time, total_out, fee,
               is_coinbase, is_coinstake, vin_count, vout_count, max_vout
        FROM txs
        ORDER BY
            CASE WHEN block_height IS NULL THEN 0 ELSE 1 END,   -- mempool first
            time DESC
        LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTxRows(rows)
}

func (s *Store) Movements(ctx context.Context, threshold float64, limit int) ([]Tx, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT txid, block_height, idx_in_block, time, total_out, fee,
               is_coinbase, is_coinstake, vin_count, vout_count, max_vout
        FROM txs
        WHERE max_vout >= ?
        ORDER BY time DESC
        LIMIT ?`, threshold, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTxRows(rows)
}

// scanTxRows assumes the column order matches the queries above.
func scanTxRows(rows *sql.Rows) ([]Tx, error) {
	var out []Tx
	for rows.Next() {
		var t Tx
		var bh sql.NullInt64
		var isCb, isCs int
		if err := rows.Scan(&t.Txid, &bh, &t.IdxInBlock, &t.Time, &t.TotalOut, &t.Fee, &isCb, &isCs, &t.VinCount, &t.VoutCount, &t.MaxVout); err != nil {
			return nil, err
		}
		if bh.Valid {
			h := bh.Int64
			t.BlockHeight = &h
		}
		t.IsCoinbase = isCb != 0
		t.IsCoinstake = isCs != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

func nullableInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
