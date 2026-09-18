#!/usr/bin/env bash
# ABOUTME: Parity guard (#646 5d/5f) — gitignore.sh, verify.sh and ci-probe.sh
# ABOUTME: under scripts/build_product_with_superspec/lib/ must be BYTE-IDENTICAL
# ABOUTME: to their build_product originals. Materialization ships only a
# ABOUTME: built-in's own scripts/<name>/ tree, so the shared shell is copied,
# ABOUTME: not sourced across workflows; this test is what stops the copies drifting.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
SRC="$DIR/../../build_product/lib"
fail=0
for base in gitignore.sh verify.sh ci-probe.sh; do
  if [ ! -f "$DIR/$base" ]; then echo "FAIL: $base missing from $DIR"; fail=1; continue; fi
  if cmp -s "$DIR/$base" "$SRC/$base"; then echo "ok: $base identical to build_product/lib/$base"; else
    echo "FAIL: $base drifted from build_product/lib/$base — copy the original over it (or fix the original first):"
    diff "$SRC/$base" "$DIR/$base" | head -20; fail=1
  fi
done
# The superspec-only helpers must NOT shadow a build_product name (a later
# copy of the same filename would silently be expected to match).
for f in "$DIR"/*.sh; do
  base="$(basename "$f")"
  case "$base" in *_test.sh|gitignore.sh|verify.sh|ci-probe.sh) continue ;; esac
  if [ -f "$SRC/$base" ]; then echo "FAIL: $base exists in build_product/lib too but is not parity-pinned — add it to this test or rename it"; fail=1; else echo "ok: $base is superspec-only"; fi
done
[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
