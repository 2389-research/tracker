#!/bin/sh
# Save current HEAD so next iteration can detect new commits
git rev-parse HEAD > ".ai/streams/${params.stream_id}/last-commit-ref.txt" 2>/dev/null || true
printf 'recorded'