set -eu
mkdir -p .ai/streams .ai/decisions .ai/worktrees .ai/gates
echo '.ai/' >> .gitignore 2>/dev/null || true
sort -u .gitignore -o .gitignore
# #553: adopt a caller-supplied `spec` file input staged by the engine to a
# fixed, deterministic path (the untrusted input value is never interpolated
# into this command; it reads the staged path directly).
if [ -f .tracker/inputs/spec ]; then
  cp .tracker/inputs/spec SPEC.md
fi
if [ ! -f SPEC.md ]; then
  echo "ERROR: SPEC.md not found in repo root."
  echo "This workflow builds from a SPEC.md describing what you want."
  echo "Get a starter one with:  tracker init build_product_with_superspec"
  echo "(creates the .dip + a SPEC.md you can edit), then re-run."
  exit 1
fi
printf 'setup-ready'