#!/bin/sh
stream_dir=".ai/streams/${params.stream_id}"
count=$(cat "$stream_dir/iteration-count.txt" 2>/dev/null || printf '0')

# First 2 iterations: sonnet for orientation
if [ "$count" -le 2 ]; then
  printf 'orientation'
  exit 0
fi

# Check if last iteration produced a commit
last_ref=$(cat "$stream_dir/last-commit-ref.txt" 2>/dev/null || printf '')
current_ref=$(git rev-parse HEAD 2>/dev/null || printf '')

if [ -n "$last_ref" ] && [ "$last_ref" = "$current_ref" ]; then
  printf 'struggling'
else
  printf 'cruising'
fi