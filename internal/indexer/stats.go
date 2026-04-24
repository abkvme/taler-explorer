package indexer

import (
	"context"
	"log/slog"
	"time"

	"taler-explorer/internal/rpc"
	"taler-explorer/internal/store"
)

type Stats struct {
	RPC      *rpc.Client
	Store    *store.Store
	Log      *slog.Logger
	Interval time.Duration
	// SupplyRefreshEvery controls how often we call the expensive
	// gettxoutsetinfo; other fields refresh on every tick.
	SupplyRefreshEvery time.Duration

	lastSupplyAt time.Time
	lastSupply   float64
}

func (s *Stats) Run(ctx context.Context) error {
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	s.once(ctx)
	// Periodically trim old snapshots to keep the table bounded.
	trim := time.NewTicker(6 * time.Hour)
	defer trim.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			s.once(ctx)
		case <-trim.C:
			cutoff := time.Now().Add(-7 * 24 * time.Hour).Unix()
			if err := s.Store.TrimStatsOlderThan(ctx, cutoff); err != nil {
				s.Log.Warn("trim stats", "err", err)
			}
		}
	}
}

func (s *Stats) once(ctx context.Context) {
	now := time.Now()
	snap := store.StatsSnapshot{TS: now.Unix()}

	if bi, err := s.RPC.GetBlockchainInfo(ctx); err == nil {
		snap.Height = bi.Blocks
		snap.DifficultyPOW = bi.Difficulty.POW
		snap.DifficultyPOS = bi.Difficulty.POS
	} else {
		s.Log.Warn("getblockchaininfo", "err", err)
	}

	if hps, err := s.RPC.GetNetworkHashPS(ctx, -1, -1); err == nil {
		snap.HashrateHPS = hps
	} else {
		s.Log.Warn("getnetworkhashps", "err", err)
	}

	if ni, err := s.RPC.GetNetworkInfo(ctx); err == nil {
		snap.PeerCount = ni.Connections
	}

	// gettxoutsetinfo is heavy; refresh at most every SupplyRefreshEvery.
	refresh := s.SupplyRefreshEvery
	if refresh == 0 {
		refresh = 15 * time.Minute
	}
	if now.Sub(s.lastSupplyAt) >= refresh || s.lastSupply == 0 {
		if tos, err := s.RPC.GetTxOutSetInfo(ctx); err == nil {
			s.lastSupply = tos.TotalAmount
			s.lastSupplyAt = now
		} else {
			s.Log.Warn("gettxoutsetinfo", "err", err)
		}
	}
	snap.Supply = s.lastSupply

	if err := s.Store.InsertStats(ctx, snap); err != nil {
		s.Log.Warn("insert stats", "err", err)
	}
}

