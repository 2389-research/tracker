#!/usr/bin/env bash
# ABOUTME: Parity guard (#646 cross-cutting) — every helper under
# ABOUTME: scripts/ask_and_execute/lib/ must be BYTE-IDENTICAL to its
# ABOUTME: build_product original. Materialization only ships a built-in's own
# ABOUTME: scripts/<name>/ tree, so the shared shell is copied, not sourced
# ABOUTME: across workflows; this test is what stops the copies from drifting.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
SRC="$DIR/../../build_product/lib"
fail=0
n=0
for f in "$DIR"/*.sh; do
  base="$(basename "$f")"
  case "$base" in *_test.sh) continue ;; esac
  n=$((n + 1))
  if [ ! -f "$SRC/$base" ]; then echo "FAIL: $base has no build_product original at $SRC"; fail=1; continue; fi
  if cmp -s "$f" "$SRC/$base"; then echo "ok: $base identical to build_product/lib/$base"; else
    echo "FAIL: $base drifted from build_product/lib/$base — copy the original over it (or fix the original first):"
    diff "$SRC/$base" "$f" | head -20; fail=1
  fi
done
[ "$n" -gt 0 ] || { echo "FAIL: no lib helpers found in $DIR"; fail=1; }
[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
