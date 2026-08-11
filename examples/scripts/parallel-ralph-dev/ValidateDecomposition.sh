#!/bin/sh
errors=""

# Check stream task files exist
for stream in a b; do
  if [ ! -f ".ai/streams/stream-$stream/task.md" ]; then
    errors="$errors\nMissing .ai/streams/stream-$stream/task.md"
  fi
done

# Check contracts exist
if [ ! -d ".ai/contracts" ] || [ -z "$(ls .ai/contracts/ 2>/dev/null)" ]; then
  errors="$errors\nMissing .ai/contracts/ directory or empty"
fi

# Check for file ownership overlaps
if [ -f .ai/streams/stream-a/task.md ] && [ -f .ai/streams/stream-b/task.md ]; then
  overlap=$(grep -h '^\- ' .ai/streams/stream-a/task.md .ai/streams/stream-b/task.md 2>/dev/null | grep -v '^\- \[' | sort | uniq -d)
  if [ -n "$overlap" ]; then
    errors="$errors\nFile ownership overlap detected: $overlap"
  fi
fi

if [ -n "$errors" ]; then
  printf 'validation_failed:%b' "$errors"
else
  printf 'valid'
fi