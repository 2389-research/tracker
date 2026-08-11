#!/bin/sh
set -e
go build ./... 2>&1
go vet ./... 2>&1
if go test ./... 2>&1; then
  printf 'tests_pass'
else
  printf 'tests_fail'
fi