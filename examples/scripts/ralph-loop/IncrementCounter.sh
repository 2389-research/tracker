#!/bin/sh
counter_file=".ai/ralph/iteration-count.txt"
mkdir -p .ai/ralph
count=$(cat "$counter_file" 2>/dev/null || echo 0)
# #646 item 9: a corrupted counter reads as 0 (dash aborts with `Illegal
# number` on a non-integer in $((...)); bash-as-sh silently reads 0).
case "$count" in ''|*[!0-9]*) count=0 ;; esac
count=$((count + 1))
printf '%d' "$count" > "$counter_file"
printf '%d' "$count"
