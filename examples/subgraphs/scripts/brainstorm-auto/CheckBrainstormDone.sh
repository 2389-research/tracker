#!/bin/sh
if grep -q 'READY_FOR_DESIGN' docs/plans/brainstorm.md 2>/dev/null; then
  printf 'done'
else
  printf 'more_questions'
fi