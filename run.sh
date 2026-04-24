#!/usr/bin/env bash
# Local dev runner for taler-explorer.
#
# Usage:
#   ./run.sh                 build and run against ./config.toml
#   ./run.sh --clean         also wipe the SQLite DB before starting
#   ./run.sh --race          build with -race
#   ./run.sh -- <args>       pass extra flags through to the binary
#
# Ctrl-C exits cleanly.

set -euo pipefail

cd "$(dirname "$0")"

CLEAN=0
RACE_FLAG=""
PASSTHRU=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean) CLEAN=1; shift ;;
    --race)  RACE_FLAG="-race"; shift ;;
    --)      shift; PASSTHRU=("$@"); break ;;
    -h|--help)
      sed -n '2,11p' "$0"
      exit 0
      ;;
    *)
      echo "unknown flag: $1" >&2
      exit 2
      ;;
  esac
done

# 1. Config: fall back to example on first run.
if [[ ! -f config.toml ]]; then
  if [[ -f config.example.toml ]]; then
    echo "No config.toml — copying config.example.toml. Edit RPC creds before using."
    cp config.example.toml config.toml
  else
    echo "No config.toml and no config.example.toml. Aborting." >&2
    exit 1
  fi
fi

# 2. Optional: wipe SQLite for a clean re-index.
DB_PATH=$(awk -F '=' '/^[[:space:]]*path[[:space:]]*=/{gsub(/[" ]/,"",$2); print $2; exit}' config.toml || true)
DB_PATH="${DB_PATH:-./taler-explorer.db}"
if [[ $CLEAN -eq 1 ]]; then
  echo "Cleaning DB at $DB_PATH (and -wal/-shm)..."
  rm -f "$DB_PATH" "${DB_PATH}-wal" "${DB_PATH}-shm"
fi

# 3. Build into an out-of-tree bin dir so the source stays tidy.
BIN_DIR="${BIN_DIR:-./.bin}"
mkdir -p "$BIN_DIR"
BIN="$BIN_DIR/taler-explorer"

echo "Building $BIN${RACE_FLAG:+ (race)}..."
go build $RACE_FLAG -o "$BIN" ./cmd/taler-explorer

# 4. Pre-flight RPC probe so config mistakes surface before we start workers.
RPC_URL=$(awk -F '=' '/^[[:space:]]*url[[:space:]]*=/{gsub(/[" ]/,"",$2); print $2; exit}' config.toml || true)
if [[ -n "${RPC_URL:-}" ]]; then
  if ! curl -sS --max-time 3 -o /dev/null "$RPC_URL" 2>/dev/null; then
    echo "Warning: RPC url $RPC_URL did not respond to a plain GET (this may be normal for JSON-RPC)."
  fi
fi

# 5. Run. Print the listen URL for convenience.
LISTEN=$(awk -F '=' '/^[[:space:]]*listen[[:space:]]*=/{gsub(/[" ]/,"",$2); print $2; exit}' config.toml || true)
LISTEN="${LISTEN:-0.0.0.0:37332}"
echo "taler-explorer listening on http://${LISTEN/0.0.0.0/127.0.0.1}/  (Ctrl-C to stop)"

exec "$BIN" -config config.toml ${PASSTHRU[@]+"${PASSTHRU[@]}"}
