set -eu
mkdir -p .ai/worktrees .ai/candidates .ai/decisions
echo '.ai/' >> .gitignore 2>/dev/null || true
sort -u .gitignore -o .gitignore
printf 'workspace-ready'