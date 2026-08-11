#!/bin/sh
set -eu
mkdir -p .tracker
COUNTER='.tracker/rework_count'
count=0
[ -f "$COUNTER" ] && count=$(cat "$COUNTER")
count=$((count + 1))
printf '%s' "$count" > "$COUNTER"
if [ "$count" -gt 2 ]; then
  printf 'budget_exhausted'
else
  printf 'budget_ok'
fi