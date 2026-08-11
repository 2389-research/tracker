#!/bin/sh
counter_file=".ai/streams/${params.stream_id}/iteration-count.txt"
if [ -f "$counter_file" ]; then
  count=$(cat "$counter_file")
else
  count=0
fi
count=$((count + 1))
printf '%d' "$count" > "$counter_file"
printf '%d' "$count"