#!/usr/bin/env bash
# prune-cache-bin.sh <bin-dir> <keep-path> <os> <arch>
#
# Removes every cached sdlc-*-<os>-<arch> binary (and its matching .signed
# sentinel) from <bin-dir> except <keep-path>, so `task deploy` doesn't pile
# up one binary per version forever. Binaries for other OS/arch combos are
# left untouched. Run AFTER the new binary is copied and signed into place,
# never before — the launcher must never see a window where no binary for
# this OS/arch exists in the cache.
#
# Usage (invoked from Taskfile.yml's deploy task):
#   bash ./scripts/prune-cache-bin.sh "$CACHE_DIR/bin" "$BIN_PATH" "$OS" "$ARCH"
set -euo pipefail

if [ "$#" -ne 4 ]; then
  echo "usage: prune-cache-bin.sh <bin-dir> <keep-path> <os> <arch>" >&2
  exit 1
fi

dir=$1
keep=$2
os=$3
arch=$4

shopt -s nullglob
for f in "$dir"/sdlc-*-"$os"-"$arch" "$dir"/sdlc-*-"$os"-"$arch".signed; do
  case "$f" in
    "$keep" | "$keep.signed") continue ;;
  esac
  rm -f "$f"
done
