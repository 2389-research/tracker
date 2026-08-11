#!/bin/sh
if [ -f docs/plans/plan.md ] && grep -q '^- \[ \]' docs/plans/plan.md 2>/dev/null; then
  printf 'resume'
else
  printf 'fresh'
fi