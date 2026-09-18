#!/bin/sh
# The iteration log is append-only LLM output, so RALPH_COMPLETE counts only
# as the LAST non-blank line (the prompt's contract) — a line quoting the
# instruction mid-log must not end the loop (#646 item 10).
log=.ai/ralph/iteration-log.md
if [ -f "$log" ] && grep -v '^[[:space:]]*$' "$log" | tail -n1 | grep -qx '[[:space:]]*RALPH_COMPLETE[[:space:]]*'; then
  printf 'complete'
else
  printf 'continue'
fi
