#!/usr/bin/env bash
# launcher_test.sh — shell test harness for bin/sdlc-launcher.sh.
#
# Exercises the download/cache/exec pipeline entirely against a PATH-stubbed
# `curl` (never a real network call, never a local HTTP fixture server, no
# spare TCP port) so the test stays deterministic on any darwin/linux box:
#   1. happy path: download, verify, cache, exec
#   2. checksum mismatch: aborted, nothing cached, fail-open exit 0
#   3. network failure: fail-open exit 0, nothing cached
#   4. re-run cache hit: no curl invocation at all on the second run
#   5. concurrent invocations: exactly one download despite the race
#
# Run with: bash internal/launcher/launcher_test.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." >/dev/null 2>&1 && pwd)"
LAUNCHER="$REPO_ROOT/bin/sdlc-launcher.sh"
PLUGIN_JSON="$REPO_ROOT/.claude-plugin/plugin.json"

PASS_COUNT=0
FAIL_COUNT=0

pass() {
  PASS_COUNT=$((PASS_COUNT + 1))
  echo "PASS: $1"
}

fail() {
  FAIL_COUNT=$((FAIL_COUNT + 1))
  echo "FAIL: $1"
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# The expected asset name is derived here with the exact same mapping the
# launcher itself uses, so the fake curl/checksums.txt content lines up with
# what the launcher will actually request and grep for.
VERSION="$(grep -m1 '"version"' "$PLUGIN_JSON" | sed -E 's/.*"version"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')"
UNAME_S="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$UNAME_S" in
  darwin) OS="darwin" ;;
  linux) OS="linux" ;;
  *)
    echo "unsupported test host OS: $(uname -s)" >&2
    exit 1
    ;;
esac
UNAME_M="$(uname -m)"
case "$UNAME_M" in
  x86_64) ARCH="amd64" ;;
  arm64 | aarch64) ARCH="arm64" ;;
  *)
    echo "unsupported test host arch: $UNAME_M" >&2
    exit 1
    ;;
esac
ASSET_NAME="sdlc-${VERSION}-${OS}-${ARCH}"

WORK_ROOT="$(mktemp -d)"
trap 'rm -rf "$WORK_ROOT"' EXIT

# install_curl_stub writes a fake `curl` into $1/curl. It reads its scenario
# and inputs from environment variables set by the caller before invoking
# the launcher (never from the network):
#   SDLC_TEST_SCENARIO      happy | mismatch | fail
#   SDLC_TEST_ASSET_NAME    exact filename the checksums.txt line must name
#   SDLC_TEST_CONTENT_FILE  file whose bytes are "downloaded" as the asset
#   SDLC_TEST_MARKER        file the stub appends one line to on every call
install_curl_stub() {
  local stub_dir="$1"
  mkdir -p "$stub_dir"
  cat >"$stub_dir/curl" <<'STUB'
#!/usr/bin/env bash
set -uo pipefail

if [ -n "${SDLC_TEST_MARKER:-}" ]; then
  echo "invoked" >>"$SDLC_TEST_MARKER"
fi

if [ "${SDLC_TEST_SCENARIO:-}" = "fail" ]; then
  echo "fake curl: simulated network failure" >&2
  exit 7
fi

output=""
prev=""
last=""
for arg in "$@"; do
  if [ "$prev" = "--output" ]; then
    output="$arg"
  fi
  prev="$arg"
  last="$arg"
done

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

case "$last" in
  */checksums.txt)
    if [ "${SDLC_TEST_SCENARIO:-}" = "mismatch" ]; then
      printf '%s  %s\n' "0000000000000000000000000000000000000000000000000000000000000000" "$SDLC_TEST_ASSET_NAME" >"$output"
    else
      hash="$(sha256_of "$SDLC_TEST_CONTENT_FILE")"
      printf '%s  %s\n' "$hash" "$SDLC_TEST_ASSET_NAME" >"$output"
    fi
    ;;
  *)
    cp "$SDLC_TEST_CONTENT_FILE" "$output"
    ;;
esac
exit 0
STUB
  chmod +x "$stub_dir/curl"
}

# make_fake_binary writes a tiny, real, executable shell script to $1 that
# proves the launcher actually exec'd it (and with which args) when run.
make_fake_binary() {
  cat >"$1" <<'BIN'
#!/bin/sh
echo "FAKE_SDLC_RAN:$*"
exit 0
BIN
  chmod +x "$1"
}

# run_launcher runs the launcher with a fresh, isolated cache dir and PATH
# (stub curl dir prepended). Sets globals: OUT, ERR, CODE, CACHE_DIR.
run_launcher() {
  local stub_dir="$1"
  shift
  CACHE_DIR="$WORK_ROOT/cache-$RANDOM$RANDOM"
  local out_file="$WORK_ROOT/out-$RANDOM$RANDOM"
  local err_file="$WORK_ROOT/err-$RANDOM$RANDOM"
  PATH="$stub_dir:$PATH" SDLC_CACHE_DIR="$CACHE_DIR" SDLC_LAUNCHER_LOCK_TIMEOUT_S=5 \
    "$LAUNCHER" "$@" >"$out_file" 2>"$err_file"
  CODE=$?
  OUT="$(cat "$out_file")"
  ERR="$(cat "$err_file")"
}

BIN_PATH_FOR() {
  echo "$1/bin/$ASSET_NAME"
}

# ---------------------------------------------------------------------------
# 1. Happy path: download, verify, cache, exec.
# ---------------------------------------------------------------------------
{
  STUB_DIR="$WORK_ROOT/stub-happy"
  install_curl_stub "$STUB_DIR"
  CONTENT_FILE="$WORK_ROOT/fake-binary-happy"
  make_fake_binary "$CONTENT_FILE"
  MARKER="$WORK_ROOT/marker-happy"

  SDLC_TEST_SCENARIO=happy SDLC_TEST_ASSET_NAME="$ASSET_NAME" \
    SDLC_TEST_CONTENT_FILE="$CONTENT_FILE" SDLC_TEST_MARKER="$MARKER" \
    run_launcher "$STUB_DIR" mcp

  BIN_PATH="$(BIN_PATH_FOR "$CACHE_DIR")"
  if [ "$CODE" -eq 0 ] && [ "$OUT" = "FAKE_SDLC_RAN:mcp" ] && [ -x "$BIN_PATH" ]; then
    pass "happy path: download+cache+exec (exit=$CODE, cached at $BIN_PATH)"
  else
    fail "happy path: expected exit 0 and FAKE_SDLC_RAN:mcp, got exit=$CODE out='$OUT' err='$ERR' bin_exists=$([ -x "$BIN_PATH" ] && echo yes || echo no)"
  fi
}

# ---------------------------------------------------------------------------
# 2. Checksum mismatch: aborted, nothing cached, fail-open exit 0.
# ---------------------------------------------------------------------------
{
  STUB_DIR="$WORK_ROOT/stub-mismatch"
  install_curl_stub "$STUB_DIR"
  CONTENT_FILE="$WORK_ROOT/fake-binary-mismatch"
  make_fake_binary "$CONTENT_FILE"

  SDLC_TEST_SCENARIO=mismatch SDLC_TEST_ASSET_NAME="$ASSET_NAME" \
    SDLC_TEST_CONTENT_FILE="$CONTENT_FILE" \
    run_launcher "$STUB_DIR" mcp

  BIN_PATH="$(BIN_PATH_FOR "$CACHE_DIR")"
  if [ "$CODE" -eq 0 ] && [ ! -e "$BIN_PATH" ] && echo "$ERR" | grep -qi "checksum mismatch"; then
    pass "checksum mismatch: fail-open exit 0, clear stderr, nothing cached"
  else
    fail "checksum mismatch: got exit=$CODE err='$ERR' bin_exists=$([ -e "$BIN_PATH" ] && echo yes || echo no)"
  fi
}

# ---------------------------------------------------------------------------
# 3. Network failure: fail-open exit 0, nothing cached, no hang.
# ---------------------------------------------------------------------------
{
  STUB_DIR="$WORK_ROOT/stub-fail"
  install_curl_stub "$STUB_DIR"

  SDLC_TEST_SCENARIO=fail run_launcher "$STUB_DIR" mcp

  BIN_PATH="$(BIN_PATH_FOR "$CACHE_DIR")"
  if [ "$CODE" -eq 0 ] && [ ! -e "$BIN_PATH" ] && echo "$ERR" | grep -qi "not ready"; then
    pass "network failure: fail-open exit 0, nothing cached"
  else
    fail "network failure: got exit=$CODE err='$ERR' bin_exists=$([ -e "$BIN_PATH" ] && echo yes || echo no)"
  fi
}

# ---------------------------------------------------------------------------
# 4. Re-run cache hit: second invocation must not call curl at all.
# ---------------------------------------------------------------------------
{
  STUB_DIR="$WORK_ROOT/stub-cachehit"
  install_curl_stub "$STUB_DIR"
  CONTENT_FILE="$WORK_ROOT/fake-binary-cachehit"
  make_fake_binary "$CONTENT_FILE"

  SDLC_TEST_SCENARIO=happy SDLC_TEST_ASSET_NAME="$ASSET_NAME" \
    SDLC_TEST_CONTENT_FILE="$CONTENT_FILE" \
    run_launcher "$STUB_DIR" mcp
  FIRST_CODE=$CODE
  FIRST_CACHE_DIR="$CACHE_DIR"

  # Second run reuses the same cache dir but a curl stub that always fails —
  # if the launcher tries to re-download, this run fails open instead of
  # exec'ing the cached binary, which is the failure signal we check for.
  MARKER="$WORK_ROOT/marker-cachehit-2"
  RERUN_OUT="$WORK_ROOT/out-cachehit-2"
  RERUN_ERR="$WORK_ROOT/err-cachehit-2"
  PATH="$STUB_DIR:$PATH" SDLC_CACHE_DIR="$FIRST_CACHE_DIR" SDLC_TEST_SCENARIO=fail \
    SDLC_TEST_MARKER="$MARKER" "$LAUNCHER" hook session-start >"$RERUN_OUT" 2>"$RERUN_ERR"
  SECOND_CODE=$?
  SECOND_OUT="$(cat "$RERUN_OUT")"

  if [ "$FIRST_CODE" -eq 0 ] && [ "$SECOND_CODE" -eq 0 ] \
    && [ "$SECOND_OUT" = "FAKE_SDLC_RAN:hook session-start" ] && [ ! -e "$MARKER" ]; then
    pass "re-run cache hit: second invocation reused cache, curl never invoked"
  else
    fail "re-run cache hit: first_exit=$FIRST_CODE second_exit=$SECOND_CODE second_out='$SECOND_OUT' marker_exists=$([ -e "$MARKER" ] && echo yes || echo no)"
  fi
}

# ---------------------------------------------------------------------------
# 5. Concurrent invocations: exactly one download despite the race.
# ---------------------------------------------------------------------------
{
  STUB_DIR="$WORK_ROOT/stub-concurrent"
  mkdir -p "$STUB_DIR"
  CONTENT_FILE="$WORK_ROOT/fake-binary-concurrent"
  make_fake_binary "$CONTENT_FILE"
  MARKER="$WORK_ROOT/marker-concurrent"
  CACHE_DIR="$WORK_ROOT/cache-concurrent"

  # A slow curl stub: sleeps before "downloading" the asset to widen the
  # race window between the two concurrent launcher invocations below.
  cat >"$STUB_DIR/curl" <<STUB
#!/usr/bin/env bash
set -uo pipefail
echo "invoked" >> "$MARKER"
output=""
prev=""
last=""
for arg in "\$@"; do
  if [ "\$prev" = "--output" ]; then output="\$arg"; fi
  prev="\$arg"
  last="\$arg"
done
case "\$last" in
  */checksums.txt)
    sleep 0.3
    hash="\$(shasum -a 256 "$CONTENT_FILE" 2>/dev/null | awk '{print \$1}')"
    if [ -z "\$hash" ]; then hash="\$(sha256sum "$CONTENT_FILE" | awk '{print \$1}')"; fi
    printf '%s  %s\n' "\$hash" "$ASSET_NAME" > "\$output"
    ;;
  *)
    sleep 0.3
    cp "$CONTENT_FILE" "\$output"
    ;;
esac
exit 0
STUB
  chmod +x "$STUB_DIR/curl"

  OUT_A="$WORK_ROOT/out-concurrent-a"
  OUT_B="$WORK_ROOT/out-concurrent-b"
  PATH="$STUB_DIR:$PATH" SDLC_CACHE_DIR="$CACHE_DIR" SDLC_LAUNCHER_LOCK_TIMEOUT_S=10 \
    "$LAUNCHER" mcp >"$OUT_A" 2>"$WORK_ROOT/err-concurrent-a" &
  PID_A=$!
  PATH="$STUB_DIR:$PATH" SDLC_CACHE_DIR="$CACHE_DIR" SDLC_LAUNCHER_LOCK_TIMEOUT_S=10 \
    "$LAUNCHER" mcp >"$OUT_B" 2>"$WORK_ROOT/err-concurrent-b" &
  PID_B=$!

  wait "$PID_A"
  CODE_A=$?
  wait "$PID_B"
  CODE_B=$?

  INVOKE_COUNT=0
  if [ -f "$MARKER" ]; then
    INVOKE_COUNT=$(wc -l <"$MARKER" | tr -d ' ')
  fi

  if [ "$CODE_A" -eq 0 ] && [ "$CODE_B" -eq 0 ] \
    && [ "$(cat "$OUT_A")" = "FAKE_SDLC_RAN:mcp" ] && [ "$(cat "$OUT_B")" = "FAKE_SDLC_RAN:mcp" ] \
    && [ "$INVOKE_COUNT" -eq 2 ]; then
    pass "concurrent invocations: exactly one download (curl invoked $INVOKE_COUNT times), both execs succeeded"
  else
    fail "concurrent invocations: code_a=$CODE_A code_b=$CODE_B out_a='$(cat "$OUT_A")' out_b='$(cat "$OUT_B")' curl_invocations=$INVOKE_COUNT (want 2)"
  fi
}

echo ""
echo "launcher_test.sh: $PASS_COUNT passed, $FAIL_COUNT failed"
if [ "$FAIL_COUNT" -gt 0 ]; then
  exit 1
fi
exit 0
