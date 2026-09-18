#!/bin/sh
# The iteration log is append-only LLM output, so RALPH_COMPLETE counts only
# when the LAST non-blank line contains it (the prompt's contract: "as the
# final line"; the models print it quoted/bold/with a suffix, so containment,
# not an exact match) — a line quoting the instruction mid-log must not end
# the loop (#646 item 10).
log=.ai/ralph/iteration-log.md
if [ -f "$log" ] && grep -v '^[[:space:]]*$' "$log" | tail -n1 | grep -q 'RALPH_COMPLETE'; then
  printf 'complete'
else
  printf 'continue'
fi
