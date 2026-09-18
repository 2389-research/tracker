#!/bin/sh
# RALPH_COMPLETE counts only when the LAST non-blank line of the append-only
# log contains it (the prompt's contract: "as the final line"; the models
# print it quoted/bold/with a suffix, so containment, not an exact match) —
# a line quoting the instruction mid-log must not end the stream (#646 item 10).
log_file=".ai/streams/${params.stream_id}/iteration-log.md"
if [ -f "$log_file" ] && grep -v '^[[:space:]]*$' "$log_file" | tail -n1 | grep -q 'RALPH_COMPLETE'; then
  printf 'complete'
else
  printf 'continue'
fi
