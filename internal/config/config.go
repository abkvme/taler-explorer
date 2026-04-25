package config

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server       ServerConfig       `toml:"server"`
	RPC          RPCConfig          `toml:"rpc"`
	DB           DBConfig           `toml:"db"`
	Indexer      IndexerConfig      `toml:"indexer"`
	Movements    MovementsConfig    `toml:"movements"`
	Transactions TransactionsConfig `toml:"transactions"`
	Network      NetworkConfig      `toml:"network"`
	Prices       PricesConfig       `toml:"prices"`
	GeoIP        GeoIPConfig        `toml:"geoip"`
	UI           UIConfig           `toml:"ui"`
}

type ServerConfig struct {
	Listen string `toml:"listen"`
}

type RPCConfig struct {
	URL            string `toml:"url"`
	User           string `toml:"user"`
	Password       string `toml:"password"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
}

func (r RPCConfig) Timeout() time.Duration {
	if r.TimeoutSeconds <= 0 {
		return 15 * time.Second
	}
	return time.Duration(r.TimeoutSeconds) * time.Second
}

type DBConfig struct {
	Path string `toml:"path"`
}

type IndexerConfig struct {
	TipPollSeconds     int   `toml:"tip_poll_seconds"`
	MempoolPollSeconds int   `toml:"mempool_poll_seconds"`
	PeersPollSeconds   int   `toml:"peers_poll_seconds"`
	StatsPollSeconds   int   `toml:"stats_poll_seconds"`
	InitialBackfill    int64 `toml:"initial_backfill"`
}

type MovementsConfig struct {
	ThresholdLow  float64 `toml:"threshold_low"`
	ThresholdMid  float64 `toml:"threshold_mid"`
	ThresholdHigh float64 `toml:"threshold_high"`
}

type TransactionsConfig struct {
	PerPage int `toml:"per_page"`
	// Color tiers by total_out (in TLR): below green_below = blue (dust),
	// below yellow_below = green, below red_below = yellow, else red.
	GreenBelow  float64 `toml:"green_below"`
	YellowBelow float64 `toml:"yellow_below"`
	RedBelow    float64 `toml:"red_below"`
}

type NetworkConfig struct {
	// Peers seen within the last HistoryHours are shown on /network.
	HistoryHours int `toml:"history_hours"`
}

type PricesConfig struct {
	Enabled         bool   `toml:"enabled"`
	Endpoint        string `toml:"endpoint"`
	Pair            string `toml:"pair"`
	QuoteSymbol     string `toml:"quote_symbol"` // e.g. "USDT" — displayed in the tile
	PollIntervalMin int    `toml:"poll_interval_minutes"`
	MarketURL       string `toml:"market_url"` // link target when tile is clicked
}

type GeoIPConfig struct {
	Enabled  bool   `toml:"enabled"`
	Endpoint string `toml:"endpoint"`
}

type UIConfig struct {
	SiteName    string `toml:"site_name"`
	CoinSymbol  string `toml:"coin_symbol"`
	AccentColor string `toml:"accent_color"`
}

func Load(path string) (*Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	applyEnvOverrides(&c)
	applyDefaults(&c)
	return &c, nil
}

func applyEnvOverrides(c *Config) {
	if v := os.Getenv("TALER_RPC_URL"); v != "" {
		c.RPC.URL = v
	}
	if v := os.Getenv("TALER_RPC_USER"); v != "" {
		c.RPC.User = v
	}
	if v := os.Getenv("TALER_RPC_PASSWORD"); v != "" {
		c.RPC.Password = v
	}
	if v := os.Getenv("TALER_LISTEN"); v != "" {
		c.Server.Listen = v
	}
	if v := os.Getenv("TALER_DB_PATH"); v != "" {
		c.DB.Path = v
	}
}

func applyDefaults(c *Config) {
	if c.Server.Listen == "" {
		c.Server.Listen = "0.0.0.0:37332"
	}
	if c.RPC.URL == "" {
		c.RPC.URL = "http://127.0.0.1:7332"
	}
	if c.DB.Path == "" {
		c.DB.Path = "./taler-explorer.db"
	}
	if c.Indexer.TipPollSeconds == 0 {
		c.Indexer.TipPollSeconds = 3
	}
	if c.Indexer.MempoolPollSeconds == 0 {
		c.Indexer.MempoolPollSeconds = 3
	}
	if c.Indexer.PeersPollSeconds == 0 {
		c.Indexer.PeersPollSeconds = 60
	}
	if c.Indexer.StatsPollSeconds == 0 {
		c.Indexer.StatsPollSeconds = 60
	}
	if c.Movements.ThresholdLow == 0 {
		c.Movements.ThresholdLow = 100
	}
	if c.Movements.ThresholdMid == 0 {
		c.Movements.ThresholdMid = 1000
	}
	if c.Movements.ThresholdHigh == 0 {
		c.Movements.ThresholdHigh = 5000
	}
	if c.Transactions.PerPage <= 0 {
		c.Transactions.PerPage = 100
	}
	if c.Transactions.GreenBelow == 0 {
		c.Transactions.GreenBelow = 100
	}
	if c.Transactions.YellowBelow == 0 {
		c.Transactions.YellowBelow = 1000
	}
	if c.Transactions.RedBelow == 0 {
		c.Transactions.RedBelow = 10000
	}
	if c.Network.HistoryHours <= 0 {
		c.Network.HistoryHours = 720 // 30 days
	}
	// Prices: opt-in, but fill sensible defaults if user enables without config.
	if c.Prices.Endpoint == "" {
		c.Prices.Endpoint = "https://qutrade.io/api/v1/market_data/"
	}
	if c.Prices.Pair == "" {
		c.Prices.Pair = "tlr_usdt"
	}
	if c.Prices.QuoteSymbol == "" {
		c.Prices.QuoteSymbol = "USDT"
	}
	if c.Prices.PollIntervalMin <= 0 {
		c.Prices.PollIntervalMin = 10
	}
	if c.Prices.MarketURL == "" {
		c.Prices.MarketURL = "https://qutrade.io/en/?market=" + c.Prices.Pair
	}
	if c.GeoIP.Endpoint == "" {
		c.GeoIP.Endpoint = "https://reallyfreegeoip.org/json/%s"
	}
	if c.UI.SiteName == "" {
		c.UI.SiteName = "Taler Explorer"
	}
	if c.UI.CoinSymbol == "" {
		c.UI.CoinSymbol = "TLR"
	}
	if c.UI.AccentColor == "" {
		c.UI.AccentColor = "#c9a24b"
	}
}
