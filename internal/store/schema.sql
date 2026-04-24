-- taler-explorer SQLite schema.
-- Run once at startup inside a single transaction.

CREATE TABLE IF NOT EXISTS blocks (
    height        INTEGER PRIMARY KEY,
    hash          TEXT    NOT NULL UNIQUE,
    prev_hash     TEXT    NOT NULL,
    time          INTEGER NOT NULL,
    tx_count      INTEGER NOT NULL,
    size          INTEGER NOT NULL,
    bits          TEXT    NOT NULL,
    difficulty    REAL    NOT NULL,
    is_pos        INTEGER NOT NULL DEFAULT 0,
    staker_address TEXT,
    miner_address  TEXT
);

CREATE INDEX IF NOT EXISTS idx_blocks_time ON blocks(time DESC);

CREATE TABLE IF NOT EXISTS txs (
    txid          TEXT PRIMARY KEY,
    block_height  INTEGER,          -- NULL while still in mempool
    idx_in_block  INTEGER,
    time          INTEGER NOT NULL,
    total_out     REAL    NOT NULL,
    fee           REAL    NOT NULL DEFAULT 0,
    is_coinbase   INTEGER NOT NULL DEFAULT 0,
    is_coinstake  INTEGER NOT NULL DEFAULT 0,
    vin_count     INTEGER NOT NULL DEFAULT 0,
    vout_count    INTEGER NOT NULL DEFAULT 0,
    max_vout      REAL    NOT NULL DEFAULT 0, -- largest single output, drives /movements
    FOREIGN KEY (block_height) REFERENCES blocks(height) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_txs_time       ON txs(time DESC);
CREATE INDEX IF NOT EXISTS idx_txs_block      ON txs(block_height);
CREATE INDEX IF NOT EXISTS idx_txs_max_vout   ON txs(max_vout DESC);

CREATE TABLE IF NOT EXISTS tx_inputs (
    txid      TEXT    NOT NULL,
    idx       INTEGER NOT NULL,      -- position in the vin array
    prev_txid TEXT,                  -- referenced tx (NULL for coinbase)
    prev_vout INTEGER,               -- referenced output (NULL for coinbase)
    coinbase  TEXT,                  -- non-empty for coinbase inputs
    PRIMARY KEY (txid, idx),
    FOREIGN KEY (txid) REFERENCES txs(txid) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_tx_inputs_prev ON tx_inputs(prev_txid, prev_vout);

CREATE TABLE IF NOT EXISTS tx_outputs (
    txid    TEXT NOT NULL,
    vout    INTEGER NOT NULL,
    address TEXT,
    amount  REAL NOT NULL,
    PRIMARY KEY (txid, vout),
    FOREIGN KEY (txid) REFERENCES txs(txid) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_tx_outputs_amount  ON tx_outputs(amount DESC);
CREATE INDEX IF NOT EXISTS idx_tx_outputs_address ON tx_outputs(address);

CREATE TABLE IF NOT EXISTS peers (
    addr         TEXT PRIMARY KEY,
    port         INTEGER,
    protocol     INTEGER,
    subver       TEXT,
    inbound      INTEGER,
    height       INTEGER,
    ping_ms      REAL,
    conn_time    INTEGER,
    country      TEXT,
    country_code TEXT,
    latitude     REAL,
    longitude    REAL,
    last_seen    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS geoip_cache (
    ip           TEXT PRIMARY KEY,
    country      TEXT,
    country_code TEXT,
    latitude     REAL,
    longitude    REAL,
    cached_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS stats_snapshots (
    ts              INTEGER PRIMARY KEY,
    supply          REAL,
    difficulty_pow  REAL,
    difficulty_pos  REAL,
    hashrate_hps    REAL,
    height          INTEGER,
    peer_count      INTEGER
);

CREATE INDEX IF NOT EXISTS idx_stats_ts ON stats_snapshots(ts DESC);

CREATE TABLE IF NOT EXISTS price_snapshots (
    ts           INTEGER NOT NULL,
    pair         TEXT    NOT NULL,
    price        REAL    NOT NULL,
    low          REAL,
    high         REAL,
    trend        REAL,
    volume_base  REAL,  -- asset_1_volume (e.g. TLR traded)
    volume_quote REAL,  -- asset_2_volume (e.g. USDT traded)
    PRIMARY KEY (pair, ts)
);

CREATE INDEX IF NOT EXISTS idx_price_ts ON price_snapshots(pair, ts DESC);
