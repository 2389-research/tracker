#!/bin/sh
base=$(git rev-parse --abbrev-ref HEAD)
for stream in a b; do
  git branch -f "feature/stream-$stream" "$base" 2>/dev/null || true
done
printf 'branches_created'