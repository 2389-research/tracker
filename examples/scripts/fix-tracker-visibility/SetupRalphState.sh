#!/bin/sh
mkdir -p .ai/ralph
if [ ! -f .ai/ralph/max-iterations.txt ]; then
  printf '15' > .ai/ralph/max-iterations.txt
fi
if [ ! -f .ai/ralph/iteration-log.md ]; then
  printf '# Iteration Log\n\n' > .ai/ralph/iteration-log.md
fi
if [ ! -f .ai/ralph/iteration-count.txt ]; then
  printf '0' > .ai/ralph/iteration-count.txt
fi
printf 'ready'