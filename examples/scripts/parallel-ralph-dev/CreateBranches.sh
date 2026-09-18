#!/bin/sh
base=$(git rev-parse --abbrev-ref HEAD)
# Record the run's base commit so ObjectiveGates diffs new code against it
# instead of assuming a branch named `main` (#646 item 11).
mkdir -p .ai
git rev-parse HEAD > .ai/base-ref.txt 2>/dev/null || true
for stream in a b; do
  git branch -f "feature/stream-$stream" "$base" 2>/dev/null || true
done
printf 'branches_created'
