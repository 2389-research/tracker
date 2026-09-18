#!/bin/sh
count=$(cat .ai/ralph/iteration-count.txt 2>/dev/null || printf '0')
max=$(cat .ai/ralph/max-iterations.txt 2>/dev/null || printf '15')
# #646 item 9: non-numeric state reads as the default (count 0 / max 15)
# instead of a `[: Illegal number` comparison error.
case "$count" in ''|*[!0-9]*) count=0 ;; esac
case "$max" in ''|*[!0-9]*) max=15 ;; esac
if [ "$count" -ge "$max" ]; then
  printf 'budget_exhausted'
else
  printf 'budget_ok'
fi
