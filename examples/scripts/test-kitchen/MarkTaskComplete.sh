#!/bin/sh
set -eu
target=$(cat docs/plans/current_task_id.txt)
sed -i.bak "s/^- \[ \] ${target}/- [x] ${target}/" docs/plans/plan.md
rm -f docs/plans/plan.md.bak
printf 'completed-%s' "$target"