#!/bin/sh
log_file=".ai/streams/${params.stream_id}/iteration-log.md"
if [ -f "$log_file" ] && grep -q 'RALPH_COMPLETE' "$log_file" 2>/dev/null; then
  printf 'complete'
else
  printf 'continue'
fi