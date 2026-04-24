package indexer

import (
	"context"
	"log/slog"
	"time"

	"taler-explorer/internal/rpc"
	"taler-explorer/internal/store"
)

type Mempool struct {
	RPC      *rpc.Client
	Store    *store.Store
	Log      *slog.Logger
	Interval time.Duration
}

func (m *Mempool) Run(ctx context.Context) error {
	t := time.NewTicker(m.Interval)
	defer t.Stop()
	m.once(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			m.once(ctx)
		}
	}
}

func (m *Mempool) once(ctx context.Context) {
	entries, err := m.RPC.GetRawMempoolVerbose(ctx)
	if err != nil {
		m.Log.Warn("getrawmempool failed", "err", err)
		return
	}
	keep := make(map[string]bool, len(entries))
	for txid, e := range entries {
		keep[txid] = true
		// Skip tx we already have (either mempool or confirmed).
		var exists int
		row := m.Store.DB.QueryRowContext(ctx, `SELECT 1 FROM txs WHERE txid = ? LIMIT 1`, txid)
		if err := row.Scan(&exists); err == nil {
			continue
		}
		// Pull full tx for outputs; some daemons return 500 for txs evicted mid-fetch.
		full, err := m.RPC.GetRawTransaction(ctx, txid)
		if err != nil {
			continue
		}
		stx, ins, outs := convertTx(full, 0, 0, entryTime(e))
		stx.BlockHeight = nil // mempool
		tx, err := m.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			m.Log.Warn("begin tx", "err", err)
			return
		}
		if err := m.Store.UpsertTx(ctx, tx, stx, ins, outs); err != nil {
			tx.Rollback()
			m.Log.Warn("upsert mempool tx", "txid", txid, "err", err)
			continue
		}
		if err := tx.Commit(); err != nil {
			m.Log.Warn("commit mempool tx", "txid", txid, "err", err)
		}
	}
	// Drop mempool rows no longer in the node mempool, and any mempool rows
	// older than 7 days as a backstop.
	if err := m.Store.PruneMempool(ctx, keep, time.Now().Add(-7*24*time.Hour).Unix()); err != nil {
		m.Log.Warn("prune mempool", "err", err)
	}
}

func entryTime(e rpc.MempoolEntry) int64 {
	if e.Time > 0 {
		return e.Time
	}
	return time.Now().Unix()
}
