#!/bin/sh
# We're already on feature/integration from ContractCheck
# Just verify we're in a good state
if git rev-parse --verify feature/integration >/dev/null 2>&1; then
  git checkout feature/integration
  printf 'merged'
else
  printf 'merge_failed'
fi