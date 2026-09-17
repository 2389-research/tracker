set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted). Fail loud if empty.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/milestones.sh"
PLAN=".ai/decisions/milestones.md"
mkdir -p .ai/build

if [ ! -s "$PLAN" ]; then
  echo "ERROR: $PLAN missing or empty — cannot reconcile declared milestone outputs"
  echo "Decompose writes per-milestone '**Files**:' lines there; without them this gate has no declaration to check."
  printf 'outputs-missing'
  exit 1
fi

# Scope the manifest to milestones actually BUILT (issue #439). This gate
# also runs on the early `accept` ship path (EscalateMilestone -accept->),
# where later milestones don't exist yet — validating the whole plan there
# flags their unbuilt dirs as missing. A milestone is "built" when its
# .ai/milestones/done/milestone-N.md marker exists (N = header number, so
# numbering gaps stay in sync with PickNextMilestone). No markers → fall
# back to the whole plan (fail safe toward catching a real skip). At the
# normal all-done entry every milestone has a marker, so behavior is
# unchanged there. The scoped slice is materialized to
# .ai/build/scoped-milestones.md for diagnosis; SCOPED_PLAN names the file
# the Files declarations are read from.
NUMBERS=$(milestone_numbers "$PLAN")
DONE_COUNT=$(count_done_milestones .ai/milestones/done)
SCOPED_PLAN="$PLAN"
if [ "$DONE_COUNT" -gt 0 ] && [ -n "$NUMBERS" ]; then
  : > .ai/build/scoped-milestones.md
  for n in $NUMBERS; do
    if [ -f ".ai/milestones/done/milestone-$n.md" ]; then
      extract_milestone "$n" "$PLAN" >> .ai/build/scoped-milestones.md
    fi
  done
  if [ -s .ai/build/scoped-milestones.md ]; then
    SCOPED_PLAN=".ai/build/scoped-milestones.md"
  fi
fi

# #640 E6: the **Files**: declarations are parsed by the ONE shared grammar
# in lib/milestones.sh (parse_files_block — shared with PickNextMilestone's
# header parser): `- **Files:** a.go`, `**Files**: a.go`, a blank line after
# the header, sub-bullets, numbered lists, inline comma lists (ALL paths),
# backticked or plain, `[a](a)` links, `(new)`/`(modify)` annotations;
# the block ends at the next `**Bold**` field or heading so sibling fields
# (`**Verify command**: go test ./cmd`) can never yield phantom paths;
# `N/A`/`none`/`—`/`(none)` are empty. declared-files.raw keeps the raw
# Files lines for diagnosis; declared-files.list is the parsed path list.
grep -iE '^[[:space:]]*([-*+][[:space:]]+)?[*_]*files[^:]*:' "$SCOPED_PLAN" > .ai/build/declared-files.raw || true
parse_files_block < "$SCOPED_PLAN" > .ai/build/declared-files.list

if [ ! -s .ai/build/declared-files.raw ]; then
  echo "ERROR: no '**Files**:' lines found in $PLAN — the Decompose output contract was violated"
  echo "First 30 lines of the plan for diagnosis:"
  head -30 "$PLAN"
  printf 'outputs-missing'
  exit 1
fi

MISSING_DIRS=""
MISSING_FILES=""
CHECKED=0
while IFS= read -r tok; do
  # Path heuristic: keep tokens with a directory separator or a dotted
  # filename; drop prose words. Skip absolute / home-anchored / parent-
  # escaping tokens outright — declared outputs are repo-relative, and
  # these must never re-anchor an existence check outside the workdir.
  # The tokens are only ever used in quoted [ -d ]/[ -e ] tests — never
  # expanded unquoted (a glob token must not pathname-expand).
  case "$tok" in
    ''|/*|~*|*..*) continue ;;
    */*|*.*) ;;
    *) continue ;;
  esac
  CHECKED=$((CHECKED + 1))
  case "$tok" in
    *[*?[]*)
      # Glob declaration (`pkg/*.go`): only its static directory prefix is
      # checkable — the files it expands to are the milestone's output.
      DIR=$(glob_static_dir "$tok")
      if [ ! -d "$DIR" ]; then
        MISSING_DIRS="$MISSING_DIRS $DIR"
      fi
      continue
      ;;
    */)
      # Trailing slash declares the DIRECTORY itself (e.g. cmd/goblin/)
      # — its absence is the structural failure class, not a file-level
      # warning, even when the parent dir exists.
      DIR="${tok%/}"
      if [ ! -d "$DIR" ]; then
        MISSING_DIRS="$MISSING_DIRS $DIR"
      fi
      continue
      ;;
    */*)
      DIR=$(dirname "$tok")
      if [ ! -d "$DIR" ]; then
        MISSING_DIRS="$MISSING_DIRS $DIR"
        continue
      fi
      ;;
  esac
  if [ ! -e "$tok" ]; then
    MISSING_FILES="$MISSING_FILES $tok"
  fi
done < .ai/build/declared-files.list

if [ "$CHECKED" -eq 0 ]; then
  echo "ERROR: Files lines exist in $PLAN but yielded zero path-like tokens — extraction is garbled"
  echo "Raw extracted declarations:"
  cat .ai/build/declared-files.raw
  printf 'outputs-missing'
  exit 1
fi

# Go stack: a declared package that exists but doesn't compile is as
# structurally absent as a missing directory. #640 D1: detect EVERY Go
# module the repo tracks (`backend/go.mod`, `go.work` + `mod/go.mod`), not
# only a root go.mod, and build each in its own directory (cheap — the
# build cache makes a second pass near-free). Outside git / untracked, a
# root go.mod still counts. testdata/vendor fixtures are not modules of
# this project. Other stacks' build gates already ran in TestMilestone and
# run again in FinalBuild — re-running them here would break the
# sub-second contract for no new signal.
BUILD_FAIL=0
GO_MODS=$(git ls-files -- 'go.mod' '*/go.mod' 2>/dev/null | grep -vE '(^|/)(testdata|vendor|node_modules)/' || true)
if [ -z "$GO_MODS" ] && [ -f go.mod ]; then
  GO_MODS="go.mod"
fi
for mod in $GO_MODS; do
  MOD_DIR=$(dirname "$mod")
  printf -- '--- go build ./... in %s (structural gate) ---\n' "$MOD_DIR"
  (cd "$MOD_DIR" && go build ./... 2>&1) || BUILD_FAIL=$?
done

if [ -n "$MISSING_DIRS" ] || [ "$BUILD_FAIL" -ne 0 ]; then
  if [ -n "$MISSING_DIRS" ]; then
    echo "ERROR: declared milestone output directories are MISSING from disk:"
    for d in $MISSING_DIRS; do printf '  - %s\n' "$d"; done | sort -u
  fi
  if [ "$BUILD_FAIL" -ne 0 ]; then
    echo "ERROR: go build ./... failed (exit $BUILD_FAIL) — see output above"
  fi
  if [ -n "$MISSING_FILES" ]; then
    echo "Declared files also not on disk (these alone would not fail the gate — may be legitimate deletions):"
    for f in $MISSING_FILES; do printf '  - %s\n' "$f"; done
  fi
  echo "A declared milestone output is structurally absent. Escalating BEFORE the review fan-out spends reviewer tokens."
  printf 'outputs-missing'
  exit 1
fi

if [ -n "$MISSING_FILES" ]; then
  echo "WARNING: declared files not on disk (NOT failing — Decompose Files lists include files to delete, and destructive milestones are expected):"
  for f in $MISSING_FILES; do printf '  - %s\n' "$f"; done
  echo "Directory-level structure and the build are intact; a reviewer can disposition these in context."
fi
echo "structural existence gate passed: $CHECKED declared path tokens reconciled"
printf 'outputs-present'
