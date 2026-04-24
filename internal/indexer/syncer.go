package indexer

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"taler-explorer/internal/rpc"
	"taler-explorer/internal/store"
)

type Syncer struct {
	RPC             *rpc.Client
	Store           *store.Store
	Log             *slog.Logger
	Interval        time.Duration
	InitialBackfill int64
}

func (s *Syncer) Run(ctx context.Context) error {
	t := time.NewTicker(s.Interval)
	defer t.Stop()

	// Run an initial pass immediately.
	s.once(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			s.once(ctx)
		}
	}
}

func (s *Syncer) once(ctx context.Context) {
	nodeHeight, err := s.RPC.GetBlockCount(ctx)
	if err != nil {
		s.Log.Warn("getblockcount failed", "err", err)
		return
	}
	localHeight, err := s.Store.LatestHeight(ctx)
	if err != nil {
		s.Log.Error("read local height", "err", err)
		return
	}

	start := localHeight + 1
	if localHeight < 0 {
		// First run — backfill a bounded window, don't try to eat the whole chain.
		start = nodeHeight - s.InitialBackfill
		if start < 0 {
			start = 0
		}
	}

	if start > nodeHeight {
		return
	}

	// Cap work per tick so we don't stall the other workers.
	const maxPerTick = 50
	end := start + maxPerTick - 1
	if end > nodeHeight {
		end = nodeHeight
	}

	// Re-org detection: confirm that our stored block at (start-1) still
	// matches the node's view; if not, walk back until it does.
	if localHeight >= 0 {
		if err := s.reorgBackIfNeeded(ctx, localHeight); err != nil {
			s.Log.Warn("reorg check failed", "err", err)
			return
		}
	}

	for h := start; h <= end; h++ {
		if err := s.ingestHeight(ctx, h); err != nil {
			s.Log.Warn("ingest height failed", "height", h, "err", err)
			return
		}
	}
}

func (s *Syncer) reorgBackIfNeeded(ctx context.Context, localHeight int64) error {
	h := localHeight
	for h >= 0 {
		localHash, err := s.Store.BlockHashAt(ctx, h)
		if err != nil {
			return err
		}
		nodeHash, err := s.RPC.GetBlockHash(ctx, h)
		if err != nil {
			return err
		}
		if localHash == nodeHash {
			return nil
		}
		s.Log.Warn("reorg detected", "height", h, "local", localHash, "node", nodeHash)
		tx, err := s.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := s.Store.DeleteBlocksFrom(ctx, tx, h); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		h--
	}
	return errors.New("reorg walked past height 0")
}

func (s *Syncer) ingestHeight(ctx context.Context, height int64) error {
	hash, err := s.RPC.GetBlockHash(ctx, height)
	if err != nil {
		return err
	}
	blk, err := s.RPC.GetBlock(ctx, hash)
	if err != nil {
		return err
	}
	return s.persistBlock(ctx, blk)
}

func (s *Syncer) persistBlock(ctx context.Context, blk *rpc.Block) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
	}()

	isPoS := blk.IsPoS()
	minerAddr, stakerAddr := firstCoinbaseAddress(blk), firstCoinstakeAddress(blk)

	sb := &store.Block{
		Height:        blk.Height,
		Hash:          blk.Hash,
		PrevHash:      blk.PreviousBlockHash,
		Time:          blk.Time,
		TxCount:       int64(len(blk.Tx)),
		Size:          blk.Size,
		Bits:          blk.Bits,
		Difficulty:    blk.Difficulty,
		IsPoS:         isPoS,
		MinerAddress:  minerAddr,
		StakerAddress: stakerAddr,
	}
	if err := s.Store.UpsertBlock(ctx, tx, sb); err != nil {
		return err
	}

	for i, t := range blk.Tx {
		stx, ins, outs := convertTx(&t, blk.Height, i, blk.Time)
		if err := s.Store.UpsertTx(ctx, tx, stx, ins, outs); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func convertTx(t *rpc.Tx, blockHeight int64, idx int, blockTime int64) (*store.Tx, []store.TxInput, []store.TxOutput) {
	var total, maxOut float64
	outs := make([]store.TxOutput, 0, len(t.Vout))
	for _, v := range t.Vout {
		total += v.Value
		if v.Value > maxOut {
			maxOut = v.Value
		}
		outs = append(outs, store.TxOutput{
			Txid:    t.Txid,
			Vout:    v.N,
			Address: v.ScriptPubKey.FirstAddress(),
			Amount:  v.Value,
		})
	}
	ins := make([]store.TxInput, 0, len(t.Vin))
	for i, v := range t.Vin {
		ins = append(ins, store.TxInput{
			Txid:     t.Txid,
			Idx:      i,
			PrevTxid: v.Txid,
			PrevVout: v.Vout,
			Coinbase: v.Coinbase,
		})
	}
	isCoinbase := len(t.Vin) == 1 && t.Vin[0].Coinbase != ""
	// Coinstake classical shape: inputs exist and first vout value == 0.
	isCoinstake := !isCoinbase && len(t.Vout) > 0 && t.Vout[0].Value == 0
	bh := blockHeight
	return &store.Tx{
		Txid:        t.Txid,
		BlockHeight: &bh,
		IdxInBlock:  idx,
		Time:        blockTime,
		TotalOut:    total,
		Fee:         0,
		IsCoinbase:  isCoinbase,
		IsCoinstake: isCoinstake,
		VinCount:    len(t.Vin),
		VoutCount:   len(t.Vout),
		MaxVout:     maxOut,
	}, ins, outs
}

func firstCoinbaseAddress(b *rpc.Block) string {
	if len(b.Tx) == 0 {
		return ""
	}
	cb := b.Tx[0]
	// Find first vout with a real address.
	for _, v := range cb.Vout {
		if a := v.ScriptPubKey.FirstAddress(); a != "" {
			return a
		}
	}
	return ""
}

func firstCoinstakeAddress(b *rpc.Block) string {
	if len(b.Tx) < 2 {
		return ""
	}
	cs := b.Tx[1]
	// In coinstake txs, vout[0] is usually the empty marker; the staker's
	// address is on vout[1].
	for i, v := range cs.Vout {
		if i == 0 && v.Value == 0 {
			continue
		}
		if a := v.ScriptPubKey.FirstAddress(); a != "" {
			return a
		}
	}
	return ""
}

// sql.ErrNoRows re-export to avoid importing database/sql in callers that only need this.
var ErrNoRows = sql.ErrNoRows
