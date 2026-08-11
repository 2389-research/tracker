#!/bin/sh
count=$(cat .ai/ralph/iteration-count.txt 2>/dev/null || printf '0')
max=$(cat .ai/ralph/max-iterations.txt 2>/dev/null || printf '10')
if [ "$count" -ge "$max" ]; then
  printf 'budget_exhausted'
else
  printf 'budget_ok'
fi