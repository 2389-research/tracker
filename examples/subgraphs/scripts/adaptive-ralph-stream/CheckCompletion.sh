#!/bin/sh
# RALPH_COMPLETE counts only as the LAST non-blank line of the append-only
# log (the prompt's contract) — a line quoting the instruction mid-log must
# not end the stream (#646 item 10).
log_file=".ai/streams/${params.stream_id}/iteration-log.md"
if [ -f "$log_file" ] && grep -v '^[[:space:]]*$' "$log_file" | tail -n1 | grep -qx '[[:space:]]*RALPH_COMPLETE[[:space:]]*'; then
  printf 'complete'
else
  printf 'continue'
fi
