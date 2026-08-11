#!/bin/sh
if ls docs/plans/*.md 1>/dev/null 2>&1; then
  archive_dir="docs/plans/archive/run-$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$archive_dir"
  for f in docs/plans/*.md; do [ -f "$f" ] && cp "$f" "$archive_dir/"; done
  rm -f docs/plans/brainstorm-context.md docs/plans/design-brief.md docs/plans/design-explore-*.md
  printf 'archived to %s' "$archive_dir"
else
  printf 'clean'
fi