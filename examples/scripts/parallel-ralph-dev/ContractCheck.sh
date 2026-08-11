#!/bin/sh
set -e
# Merge stream branches to a temporary integration branch
base=$(git rev-parse --abbrev-ref HEAD)
git checkout -b feature/integration "$base" 2>/dev/null || git checkout feature/integration

# Merge each stream
merge_ok=true
for stream in a b; do
  if ! git merge --no-ff "feature/stream-$stream" -m "integrate stream-$stream" 2>/dev/null; then
    merge_ok=false
    git merge --abort 2>/dev/null || true
    break
  fi
done

if [ "$merge_ok" = false ]; then
  git checkout "$base" 2>/dev/null || true
  printf 'merge_conflict'
  exit 0
fi

# Run build and contract tests
if go build ./... 2>&1 && go test ./... 2>&1; then
  printf 'contracts_pass'
else
  printf 'contracts_fail'
fi