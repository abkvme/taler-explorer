package rpc

import (
	"context"
	"encoding/json"
	"strconv"
)

// Difficulty handles Taler's hybrid shape where `difficulty` in
// getblockchaininfo / getmininginfo is an object
// {"proof-of-work":x,"proof-of-stake":y,"search-interval":z}. On non-Taler
// forks it arrives as a plain float, which we still accept (both go into POW).
type Difficulty struct {
	POW            float64
	POS            float64
	SearchInterval float64
}

func (d *Difficulty) UnmarshalJSON(data []byte) error {
	// Try plain number first.
	if f, err := strconv.ParseFloat(string(data), 64); err == nil {
		d.POW = f
		return nil
	}
	var obj struct {
		POW            float64 `json:"proof-of-work"`
		POS            float64 `json:"proof-of-stake"`
		SearchInterval float64 `json:"search-interval"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	d.POW = obj.POW
	d.POS = obj.POS
	d.SearchInterval = obj.SearchInterval
	return nil
}

// BlockchainInfo is a subset of the getblockchaininfo response that we care
// about. Taler adds pow/pos difficulty split fields (see
// src/rpc/blockchain.cpp:65-82); if they are absent we fall back to Difficulty.
type BlockchainInfo struct {
	Chain                 string     `json:"chain"`
	Blocks                int64      `json:"blocks"`
	Headers               int64      `json:"headers"`
	BestBlockHash         string     `json:"bestblockhash"`
	Difficulty            Difficulty `json:"difficulty"`
	MedianTime            int64      `json:"mediantime"`
	VerificationProgress  float64    `json:"verificationprogress"`
	InitialBlockDownload  bool       `json:"initialblockdownload"`
	SizeOnDisk            int64      `json:"size_on_disk"`
}

type NetworkInfo struct {
	Version         int     `json:"version"`
	Subversion      string  `json:"subversion"`
	ProtocolVersion int     `json:"protocolversion"`
	Connections     int     `json:"connections"`
	RelayFee        float64 `json:"relayfee"`
}

type TxOutSetInfo struct {
	Height      int64   `json:"height"`
	TotalAmount float64 `json:"total_amount"`
	TxOuts      int64   `json:"txouts"`
}

type PeerInfo struct {
	ID             int64   `json:"id"`
	Addr           string  `json:"addr"`
	Services       string  `json:"services"`
	LastSend       int64   `json:"lastsend"`
	LastRecv       int64   `json:"lastrecv"`
	ConnTime       int64   `json:"conntime"`
	PingTime       float64 `json:"pingtime"`
	Version        int     `json:"version"`
	Subver         string  `json:"subver"`
	Inbound        bool    `json:"inbound"`
	StartingHeight int64   `json:"startingheight"`
	SyncedHeaders  int64   `json:"synced_headers"`
	SyncedBlocks   int64   `json:"synced_blocks"`
}

type Block struct {
	Hash              string `json:"hash"`
	Confirmations     int64  `json:"confirmations"`
	Size              int64  `json:"size"`
	Height            int64  `json:"height"`
	Version           int    `json:"version"`
	MerkleRoot        string `json:"merkleroot"`
	Time              int64  `json:"time"`
	MedianTime        int64  `json:"mediantime"`
	Nonce             uint64 `json:"nonce"`
	Bits              string `json:"bits"`
	Difficulty        float64 `json:"difficulty"`
	PreviousBlockHash string `json:"previousblockhash"`
	NextBlockHash     string `json:"nextblockhash"`
	// verbose=2 inlines full tx objects.
	Tx []Tx `json:"tx"`
	// Taler PoS indicators (see src/primitives/block.h).
	Flags     string `json:"flags"`
	ProofType string `json:"proof_type"`
}

// IsPoS returns true if the block looks like a proof-of-stake block. The
// heuristic covers both explicit `proof_type` / `flags` fields (if the daemon
// exposes them) and the classical PoS shape: >=2 txs, tx[0] coinbase-empty,
// tx[1] coinstake-like (first vout empty).
func (b *Block) IsPoS() bool {
	if b.ProofType == "pos" || b.ProofType == "proof-of-stake" {
		return true
	}
	if b.Flags != "" && (b.Flags == "proof-of-stake" || containsFlag(b.Flags, "proof-of-stake")) {
		return true
	}
	if len(b.Tx) >= 2 {
		if len(b.Tx[1].Vout) >= 1 && b.Tx[1].Vout[0].Value == 0 {
			return true
		}
	}
	return false
}

func containsFlag(flags, needle string) bool {
	for i := 0; i+len(needle) <= len(flags); i++ {
		if flags[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

type Tx struct {
	Txid     string    `json:"txid"`
	Hash     string    `json:"hash"`
	Version  int       `json:"version"`
	Size     int64     `json:"size"`
	VSize    int64     `json:"vsize"`
	LockTime int64     `json:"locktime"`
	Vin      []Vin     `json:"vin"`
	Vout     []Vout    `json:"vout"`
	Hex      string    `json:"hex"`
	// Present on verbose=1 via getrawtransaction:
	BlockHash     string `json:"blockhash"`
	Confirmations int64  `json:"confirmations"`
	Time          int64  `json:"time"`
	BlockTime     int64  `json:"blocktime"`
}

type Vin struct {
	Txid      string `json:"txid"`
	Vout      int    `json:"vout"`
	Coinbase  string `json:"coinbase"`
	Sequence  uint64 `json:"sequence"`
}

type Vout struct {
	Value        float64       `json:"value"`
	N            int           `json:"n"`
	ScriptPubKey ScriptPubKey  `json:"scriptPubKey"`
}

type ScriptPubKey struct {
	Asm       string   `json:"asm"`
	Hex       string   `json:"hex"`
	ReqSigs   int      `json:"reqSigs"`
	Type      string   `json:"type"`
	Addresses []string `json:"addresses"`
	Address   string   `json:"address"`
}

// FirstAddress returns the single "best" address associated with the output.
// Newer daemons emit a single `address`; older ones emit `addresses[]`.
func (s ScriptPubKey) FirstAddress() string {
	if s.Address != "" {
		return s.Address
	}
	if len(s.Addresses) > 0 {
		return s.Addresses[0]
	}
	return ""
}

// MempoolEntry is one element of getrawmempool verbose=true.
type MempoolEntry struct {
	Size     int64   `json:"size"`
	Fee      float64 `json:"fee"`
	Time     int64   `json:"time"`
	Height   int64   `json:"height"`
	Depends  []string `json:"depends"`
}

// ---- typed wrappers ----

func (c *Client) GetBlockCount(ctx context.Context) (int64, error) {
	var h int64
	return h, c.Call(ctx, "getblockcount", nil, &h)
}

func (c *Client) GetBlockHash(ctx context.Context, height int64) (string, error) {
	var h string
	return h, c.Call(ctx, "getblockhash", []any{height}, &h)
}

// GetBlock fetches a block at verbose level 2 (full tx objects inlined).
func (c *Client) GetBlock(ctx context.Context, hash string) (*Block, error) {
	var b Block
	if err := c.Call(ctx, "getblock", []any{hash, 2}, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (c *Client) GetBlockchainInfo(ctx context.Context) (*BlockchainInfo, error) {
	var bi BlockchainInfo
	return &bi, c.Call(ctx, "getblockchaininfo", nil, &bi)
}

func (c *Client) GetNetworkInfo(ctx context.Context) (*NetworkInfo, error) {
	var ni NetworkInfo
	return &ni, c.Call(ctx, "getnetworkinfo", nil, &ni)
}

// GetNetworkHashPS returns network hash/sec. blocks=-1 averages over the last
// difficulty change; height=-1 means at tip.
func (c *Client) GetNetworkHashPS(ctx context.Context, blocks, height int) (float64, error) {
	var hps float64
	return hps, c.Call(ctx, "getnetworkhashps", []any{blocks, height}, &hps)
}

// GetTxOutSetInfo is expensive on large chains; call infrequently.
func (c *Client) GetTxOutSetInfo(ctx context.Context) (*TxOutSetInfo, error) {
	var tos TxOutSetInfo
	return &tos, c.Call(ctx, "gettxoutsetinfo", nil, &tos)
}

func (c *Client) GetPeerInfo(ctx context.Context) ([]PeerInfo, error) {
	var p []PeerInfo
	return p, c.Call(ctx, "getpeerinfo", nil, &p)
}

// GetRawMempoolVerbose returns map[txid]MempoolEntry.
func (c *Client) GetRawMempoolVerbose(ctx context.Context) (map[string]MempoolEntry, error) {
	var m map[string]MempoolEntry
	return m, c.Call(ctx, "getrawmempool", []any{true}, &m)
}

// GetRawTransaction verbose=1.
func (c *Client) GetRawTransaction(ctx context.Context, txid string) (*Tx, error) {
	var t Tx
	if err := c.Call(ctx, "getrawtransaction", []any{txid, 1}, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Raw is an escape hatch for ad-hoc calls.
func (c *Client) Raw(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	var out json.RawMessage
	return out, c.Call(ctx, method, params, &out)
}
