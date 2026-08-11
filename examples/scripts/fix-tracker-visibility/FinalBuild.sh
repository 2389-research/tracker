#!/bin/sh
set -e
cd "$(git rev-parse --show-toplevel)"
go build ./... 2>&1
go vet ./... 2>&1
printf 'build_pass'