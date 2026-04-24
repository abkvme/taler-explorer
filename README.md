# taler-explorer

[![CI](https://github.com/abkvme/taler-explorer/actions/workflows/ci.yml/badge.svg)](https://github.com/abkvme/taler-explorer/actions/workflows/ci.yml)
[![Release](https://github.com/abkvme/taler-explorer/actions/workflows/release.yml/badge.svg)](https://github.com/abkvme/taler-explorer/actions/workflows/release.yml)
[![Container](https://img.shields.io/badge/ghcr.io-abkvme%2Ftaler--explorer-blue?logo=docker)](https://github.com/abkvme/taler-explorer/pkgs/container/taler-explorer)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A lightweight, single-binary **block explorer for [Taler](https://github.com/abkvme/taler)** — a hybrid PoW (Lyra2Z) / PoS Litecoin fork.

* **Modern animated UI** — dark glassmorphism, live sparklines, live peer map, responsive header grid.
* **No external dependencies at runtime** — pure-Go SQLite (`modernc.org/sqlite`), no cgo, cross-compiles cleanly.
* **No public API** — HTML-rendered pages plus private `/_partial/*` endpoints consumed only by the site itself for live updates.
* **Talks to `talerd` over standard bitcoin-compatible JSON-RPC** (default port 7332).

## Pages

| Route | Description |
|---|---|
| `/` | Latest blocks (height, type, txs, size, difficulty, hash). |
| `/txs` | Paginated transactions with color-coded amount tiers (blue → green → yellow → red). |
| `/blocks/{height}` | Block detail: PoW/PoS type, txs in block, miner/staker address. |
| `/txs/{txid}` | Tx detail with inputs, outputs, implied fee, address links. |
| `/address/{addr}` | Address summary (received / sent / net) + paginated history. |
| `/network` | Every peer seen in the last N hours, toggle between **list** and **world map** views. |
| `/movements` | Large-transaction watcher (iquidus-style) with tier thresholds. |
| `/search?q=` | Classifies the query (block #, tx hash, or address) and redirects. |

A live header on every page shows **network hashrate** (with a 24 h sparkline), **difficulty** (PoW + PoS split), **supply**, **price** (with a 24 h sparkline, links to qutrade), **market cap**, and **block height + peer count**.

## Stack

* Go 1.25+
* `net/http` + `chi` router, `html/template`
* Pure-Go SQLite (`modernc.org/sqlite`, no cgo)
* HTMX + Alpine.js + hand-rolled CSS (no Node build step)
* uPlot (inline hashrate + price sparklines)
* Leaflet + OpenStreetMap tiles (peer world map)

## Run locally

```sh
cp config.example.toml config.toml
# edit [rpc] user / password to match your talerd
./run.sh                     # builds into ./.bin/, serves on :37332
./run.sh --clean             # wipe SQLite + rebuild from scratch
./run.sh --race              # -race build for dev
```

Your `talerd` must be started with `-server=1 -rpcuser=X -rpcpassword=Y` (see [`config.example.toml`](config.example.toml) for the explorer side).

## Run via Docker

```sh
cp config.example.toml config.toml
# edit [rpc] credentials

docker compose up -d
# open http://localhost:37332/
```

The compose file maps `host.docker.internal` so the container reaches a `talerd` running on the Docker host.

Pre-built images on every tag release:

```
docker pull ghcr.io/abkvme/taler-explorer:latest
docker pull ghcr.io/abkvme/taler-explorer:v0.1.0
```

## Configuration

All settings live in `config.toml` (see [`config.example.toml`](config.example.toml) for the full reference). Environment variable overrides:

| Env var | Overrides |
|---|---|
| `TALER_RPC_URL` | `[rpc].url` |
| `TALER_RPC_USER` | `[rpc].user` |
| `TALER_RPC_PASSWORD` | `[rpc].password` |
| `TALER_LISTEN` | `[server].listen` |
| `TALER_DB_PATH` | `[db].path` |

## Releases

Tag-driven: push a `v*` tag and the release workflow publishes a multi-arch container to `ghcr.io/abkvme/taler-explorer` and creates a GitHub Release with the pull command.

```sh
git tag v0.1.0
git push origin v0.1.0
```

## License

MIT — see [LICENSE](LICENSE).
