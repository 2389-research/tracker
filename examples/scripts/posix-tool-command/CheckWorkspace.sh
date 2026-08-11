#!/bin/sh
set -eu
if [ -f go.mod ]; then
  printf 'workspace-ready\n'
else
  printf 'workspace-missing\n'
fi