#!/usr/bin/env bash
# ABOUTME: Builds the agent-runner binary and Docker image for SWE-bench.
# ABOUTME: Cross-compiles for linux/amd64 and bakes into tracker-swebench-base image.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "==> Cross-compiling agent-runner for linux/amd64..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o agent-runner ./agent-runner/

echo "==> Building Docker image: tracker-swebench-base..."
docker build --platform linux/amd64 -t tracker-swebench-base .

echo "==> Cleaning up agent-runner binary..."
rm -f agent-runner

echo "==> Done. Image: tracker-swebench-base"
echo "    Run: go build -o tracker-swebench . && ./tracker-swebench --dataset <path> --model <model>"
