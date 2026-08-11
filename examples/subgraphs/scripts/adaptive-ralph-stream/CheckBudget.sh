#!/bin/sh
stream_dir=".ai/streams/${params.stream_id}"
count=$(cat "$stream_dir/iteration-count.txt" 2>/dev/null || printf '0')
max=$(cat "$stream_dir/max-iterations.txt" 2>/dev/null || printf '8')
if [ "$count" -ge "$max" ]; then
  printf 'budget_exhausted'
else
  printf 'budget_ok'
fi