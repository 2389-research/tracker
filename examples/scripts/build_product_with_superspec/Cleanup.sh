set -eu
rm -rf .ai/worktrees .ai/streams .ai/gates
# Preserve decision log
echo "=== Preserved ==="
ls .ai/decisions/
printf 'cleanup-done'