#!/bin/sh
if [ -f .ai/ralph/iteration-log.md ] && grep -q 'RALPH_COMPLETE' .ai/ralph/iteration-log.md 2>/dev/null; then
  printf 'complete'
else
  printf 'continue'
fi