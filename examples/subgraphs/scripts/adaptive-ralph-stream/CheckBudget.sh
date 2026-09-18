#!/bin/sh
stream_dir=".ai/streams/${params.stream_id}"
count=$(cat "$stream_dir/iteration-count.txt" 2>/dev/null || printf '0')
max=$(cat "$stream_dir/max-iterations.txt" 2>/dev/null || printf '8')
# #646 item 9: non-numeric state reads as the default instead of a
# `[: Illegal number` comparison error.
case "$count" in ''|*[!0-9]*) count=0 ;; esac
case "$max" in ''|*[!0-9]*) max=8 ;; esac
if [ "$count" -ge "$max" ]; then
  printf 'budget_exhausted'
else
  printf 'budget_ok'
fi
