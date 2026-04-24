package store

type Block struct {
	Height        int64
	Hash          string
	PrevHash      string
	Time          int64
	TxCount       int64
	Size          int64
	Bits          string
	Difficulty    float64
	IsPoS         bool
	StakerAddress string
	MinerAddress  string
}

type Tx struct {
	Txid        string
	BlockHeight *int64
	IdxInBlock  int
	Time        int64
	TotalOut    float64
	Fee         float64
	IsCoinbase  bool
	IsCoinstake bool
	VinCount    int
	VoutCount   int
	MaxVout     float64
}

type TxOutput struct {
	Txid    string
	Vout    int
	Address string
	Amount  float64
}

// TxInput carries both the raw (prev_txid, prev_vout) reference and — when we
// have the previous tx in our own store — the resolved spender address and
// amount. Coinbase inputs have empty PrevTxid and a non-empty Coinbase string.
type TxInput struct {
	Txid     string
	Idx      int
	PrevTxid string
	PrevVout int
	Coinbase string

	// Resolved from tx_outputs. Zero values when we haven't indexed the
	// referenced tx (common for legacy data pre-this-change).
	Address  string
	Amount   float64
	Resolved bool
}

type Peer struct {
	Addr        string
	Port        int
	Protocol    int
	Subver      string
	Inbound     bool
	Height      int64
	PingMs      float64
	ConnTime    int64
	Country     string
	CountryCode string
	Latitude    float64
	Longitude   float64
	LastSeen    int64
}

type StatsSnapshot struct {
	TS            int64
	Supply        float64
	DifficultyPOW float64
	DifficultyPOS float64
	HashrateHPS   float64
	Height        int64
	PeerCount     int
}
