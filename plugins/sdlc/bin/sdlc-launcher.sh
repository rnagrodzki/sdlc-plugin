#!/usr/bin/env bash
# sdlc-launcher.sh — resolves, downloads (on first use), verifies, and execs
# the platform-specific "sdlc" binary for the plugin's own version.
#
# Invoked by:
#   .mcp.json          -> args: ["mcp"]
#   hooks/hooks.json    -> args: ["hook", "<name>"]
#
# Contract: this script must never hang and must never leave a Claude Code
# session blocked. Any failure while resolving/downloading/verifying the
# binary (missing plugin.json, unsupported checksum, network failure, lock
# contention that never clears) fails OPEN: it prints a short diagnostic to
# stderr and exits 0. Hooks then degrade silently and MCP simply retries on
# the next connect attempt. The one exception is an unsupported OS/arch,
# which is a permanent local condition (not a transient fetch failure) and
# so fails hard with a nonzero exit instead.
set -uo pipefail

REPO="rnagrodzki/sdlc-plugin"
CACHE_DIR="${SDLC_CACHE_DIR:-$HOME/.sdlc-cache}"
BIN_DIR="$CACHE_DIR/bin"
FAIL_OPEN_MSG="sdlc binary not ready — run any sdlc tool or re-open session"

# Timeout budgets are mode-dependent. "hook" invocations run inside a
# Claude Code hook budget as tight as 3000ms (see hooks/hooks.json) — on a
# cold cache they must fail open fast rather than attempt a real download,
# so a stalled/slow network degrades to the not-ready message instead of
# the hook being killed mid-download. "mcp" connects have no such budget
# and are the path relied on to actually complete a cold download (a hook
# that fails open now succeeds on the next MCP connect once the fetch has
# finished in the background of a later invocation).
case "${1:-}" in
  hook)
    DEFAULT_LOCK_TIMEOUT_S=1
    DEFAULT_CURL_CONNECT_TIMEOUT_S=1
    DEFAULT_CURL_MAX_TIME_S=1
    ;;
  *)
    # The release binary itself (not just this script) runs ~15MB
    # unstripped — on a slow connection (corporate VPN, hotel wifi, ~5-10
    # Mbps) a cold download alone can take 15-25s. 15s here left no margin
    # and would fail-open (never completing) on such networks. 60s gives
    # real headroom down to ~2 Mbps; the matching lock timeout is set just
    # above it so a concurrent connect waits for an in-flight download to
    # finish instead of needlessly failing open and re-fetching later.
    DEFAULT_LOCK_TIMEOUT_S=65
    DEFAULT_CURL_CONNECT_TIMEOUT_S=5
    DEFAULT_CURL_MAX_TIME_S=60
    ;;
esac
LOCK_TIMEOUT_S="${SDLC_LAUNCHER_LOCK_TIMEOUT_S:-$DEFAULT_LOCK_TIMEOUT_S}"
CURL_CONNECT_TIMEOUT_S="${SDLC_LAUNCHER_CURL_CONNECT_TIMEOUT_S:-$DEFAULT_CURL_CONNECT_TIMEOUT_S}"
CURL_MAX_TIME_S="${SDLC_LAUNCHER_CURL_MAX_TIME_S:-$DEFAULT_CURL_MAX_TIME_S}"
LOCK_POLL_S=0.2

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
PLUGIN_ROOT="$(cd "$SCRIPT_DIR/.." >/dev/null 2>&1 && pwd)"
PLUGIN_JSON="$PLUGIN_ROOT/.claude-plugin/plugin.json"

# fail_open prints an optional detail line plus the standard fail-open
# message, then exits 0 so the caller (a hook or MCP connect) never blocks.
fail_open() {
  if [ -n "${1:-}" ]; then
    echo "sdlc-launcher: $1" >&2
  fi
  echo "$FAIL_OPEN_MSG" >&2
  exit 0
}

# fail_hard is only used for permanent, non-transient local problems
# (unsupported OS/arch) where retrying later can never help.
fail_hard() {
  echo "sdlc-launcher: $1" >&2
  exit 1
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# --- 1. VERSION (from .claude-plugin/plugin.json, no jq dependency) --------
if [ ! -f "$PLUGIN_JSON" ]; then
  fail_open "plugin.json not found at $PLUGIN_JSON"
fi

VERSION="$(grep -m1 '"version"' "$PLUGIN_JSON" | sed -E 's/.*"version"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')"
if [ -z "$VERSION" ]; then
  fail_open "could not read version from $PLUGIN_JSON"
fi

# --- OS/arch detection ------------------------------------------------------
UNAME_S="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$UNAME_S" in
  darwin) OS="darwin" ;;
  linux) OS="linux" ;;
  *) fail_hard "unsupported OS: $(uname -s)" ;;
esac

UNAME_M="$(uname -m)"
case "$UNAME_M" in
  x86_64) ARCH="amd64" ;;
  arm64 | aarch64) ARCH="arm64" ;;
  *) fail_hard "unsupported architecture: $UNAME_M" ;;
esac

# --- 2. BIN ------------------------------------------------------------------
ASSET="sdlc-${VERSION}-${OS}-${ARCH}"
BIN_PATH="$BIN_DIR/$ASSET"

# --- 3. download if missing ---------------------------------------------------
if [ ! -x "$BIN_PATH" ]; then
  mkdir -p "$BIN_DIR" 2>/dev/null || fail_open "could not create cache dir $BIN_DIR"

  # Known limitation: mkdir-based locks have no owner/liveness check. If a
  # launcher holding this lock is SIGKILLed, the trap below never fires and
  # LOCK_DIR is left behind permanently. Every later invocation then waits
  # out LOCK_TIMEOUT_S and fails open (safe — never hangs a session) until
  # someone manually removes LOCK_DIR. No automatic staleness recovery is
  # implemented; a PID-liveness check would add real complexity for a rare
  # failure mode (a launcher killed mid-download), so this is accepted as-is.
  LOCK_DIR="$BIN_DIR/.fetch-${VERSION}.lock"
  LOCK_ACQUIRED=0
  LOCK_WAITED_MS=0
  LOCK_TIMEOUT_MS=$((LOCK_TIMEOUT_S * 1000))
  LOCK_POLL_MS=200

  while [ "$LOCK_WAITED_MS" -lt "$LOCK_TIMEOUT_MS" ]; do
    if mkdir "$LOCK_DIR" 2>/dev/null; then
      LOCK_ACQUIRED=1
      break
    fi
    sleep "$LOCK_POLL_S"
    LOCK_WAITED_MS=$((LOCK_WAITED_MS + LOCK_POLL_MS))
  done

  if [ "$LOCK_ACQUIRED" -ne 1 ]; then
    fail_open "timed out waiting for fetch lock $LOCK_DIR"
  fi

  # From here on, release the lock on every exit path (success, error, or an
  # interrupt) so a killed launcher never leaves a permanently stuck lock.
  trap 'rmdir "$LOCK_DIR" 2>/dev/null || true' EXIT INT TERM

  # A concurrent fetcher may have finished the download while we were
  # waiting for the lock — recheck before fetching again.
  if [ ! -x "$BIN_PATH" ]; then
    TMP_DIR="$(mktemp -d "$BIN_DIR/.tmp-XXXXXX" 2>/dev/null)" || fail_open "could not create temp dir under $BIN_DIR"

    ASSET_URL="https://github.com/${REPO}/releases/download/v${VERSION}/${ASSET}"
    CHECKSUMS_URL="https://github.com/${REPO}/releases/download/v${VERSION}/checksums.txt"
    TMP_BIN="$TMP_DIR/$ASSET"
    TMP_SUMS="$TMP_DIR/checksums.txt"

    if ! curl --fail --silent --show-error --location \
      --connect-timeout "$CURL_CONNECT_TIMEOUT_S" --max-time "$CURL_MAX_TIME_S" \
      --output "$TMP_BIN" "$ASSET_URL"; then
      rm -rf "$TMP_DIR"
      fail_open "download failed: $ASSET_URL"
    fi

    if ! curl --fail --silent --show-error --location \
      --connect-timeout "$CURL_CONNECT_TIMEOUT_S" --max-time "$CURL_MAX_TIME_S" \
      --output "$TMP_SUMS" "$CHECKSUMS_URL"; then
      rm -rf "$TMP_DIR"
      fail_open "download failed: $CHECKSUMS_URL"
    fi

    EXPECTED_SUM="$(grep " ${ASSET}\$" "$TMP_SUMS" 2>/dev/null | awk '{print $1}' | head -n1)"
    if [ -z "$EXPECTED_SUM" ]; then
      rm -rf "$TMP_DIR"
      fail_open "no checksum entry for $ASSET in checksums.txt"
    fi

    ACTUAL_SUM="$(sha256_of "$TMP_BIN")"
    if [ "$ACTUAL_SUM" != "$EXPECTED_SUM" ]; then
      rm -rf "$TMP_DIR"
      fail_open "checksum mismatch for $ASSET (expected $EXPECTED_SUM, got $ACTUAL_SUM) — download aborted, nothing cached"
    fi

    chmod +x "$TMP_BIN" # must happen before the rename — see step 4
    mv "$TMP_BIN" "$BIN_PATH" # same filesystem as TMP_DIR -> atomic rename
    rm -rf "$TMP_DIR"
  fi

  rmdir "$LOCK_DIR" 2>/dev/null || true
  trap - EXIT INT TERM
fi

# --- 4. exec -------------------------------------------------------------------
if [ ! -x "$BIN_PATH" ]; then
  fail_open "cached binary missing or not executable: $BIN_PATH"
fi

exec "$BIN_PATH" "$@"
