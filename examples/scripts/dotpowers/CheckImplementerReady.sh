#!/bin/sh
if grep -q 'READY_TO_IMPLEMENT' docs/plans/implementer-status.txt 2>/dev/null; then
  printf 'ready'
else
  printf 'has_question'
fi