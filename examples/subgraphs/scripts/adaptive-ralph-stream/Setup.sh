#!/bin/sh
stream_dir=".ai/streams/${params.stream_id}"
mkdir -p "$stream_dir"
if [ ! -f "$stream_dir/max-iterations.txt" ]; then
  printf '${params.max_iterations}' > "$stream_dir/max-iterations.txt"
fi
if [ ! -f "$stream_dir/iteration-count.txt" ]; then
  printf '0' > "$stream_dir/iteration-count.txt"
fi
if [ ! -f "$stream_dir/iteration-log.md" ]; then
  printf '# Stream ${params.stream_id} — Iteration Log\n\n' > "$stream_dir/iteration-log.md"
fi
# Store initial commit ref for velocity tracking
git rev-parse HEAD > "$stream_dir/last-commit-ref.txt" 2>/dev/null || true
printf 'ready'