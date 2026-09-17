set -eu
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
# flags their unbuilt dirs as missing. DONE_COUNT is the number of markers
# PickNextMilestone wrote to .ai/milestones/done. 0/unreadable → fall back
# to the whole plan (fail safe toward catching a real skip). At the normal
# all-done entry DONE_COUNT == TOTAL, so behavior is unchanged there.
DONE_COUNT=$(ls -1 .ai/milestones/done 2>/dev/null | wc -l | tr -d ' ')
case "$DONE_COUNT" in ''|*[!0-9]*) DONE_COUNT=0 ;; esac
SCOPED_PLAN="$PLAN"
if [ "$DONE_COUNT" -gt 0 ]; then
  awk -v last="$DONE_COUNT" '
    /^#+ *[Mm]ilestone *[0-9]+/ {
      match($0, /[0-9]+/); n = substr($0, RSTART, RLENGTH) + 0
      keep = (n >= 1 && n <= last)
    }
    keep { print }
  ' "$PLAN" > .ai/build/scoped-milestones.md
  if [ -s .ai/build/scoped-milestones.md ]; then
    SCOPED_PLAN=".ai/build/scoped-milestones.md"
  fi
fi

# Extract the **Files**: declarations. LLM-written file: headers vary
# ("**Files**:", "Files:", "- **Files:** ..."), and the list may be
# inline on the header line or a bulleted block underneath it. A blank
# or non-bullet line ends a block.
awk '
  /^[[:space:]]*[-*]?[[:space:]]*\*{0,2}[Ff]iles\*{0,2}[[:space:]]*:/ {
    infiles = 1
    line = $0
    sub(/^[^:]*:[[:space:]]*/, "", line)
    if (length(line) > 0) print line
    next
  }
  infiles && /^[[:space:]]*[-*+][[:space:]]/ { print; next }
  { infiles = 0 }
' "$SCOPED_PLAN" > .ai/build/declared-files.raw

if [ ! -s .ai/build/declared-files.raw ]; then
  echo "ERROR: no '**Files**:' lines found in $PLAN — the Decompose output contract was violated"
  echo "First 30 lines of the plan for diagnosis:"
  head -30 "$PLAN"
  printf 'outputs-missing'
  exit 1
fi

# Parse ONE path per bullet (issue #440): prefer the FIRST backticked
# token; else strip inline prose after the first `(` or `#`, drop the
# bullet marker, and take the first whitespace token. Whitespace-tokenizing
# the whole bullet turned prose ("Deps struct", "go.sum", "NewRepo(t)")
# into phantom paths. The single-quoted sed expressions make the backticks
# literal (sh does not run command-substitution inside single quotes), and
# the extracted tokens are only ever used in quoted [ -d ]/[ -e ] tests.
: > .ai/build/declared-files.list
while IFS= read -r line; do
  tok=$(printf '%s\n' "$line" | sed -n 's/^[^`]*`\([^`]*\)`.*/\1/p' | head -1)
  if [ -z "$tok" ]; then
    tok=$(printf '%s\n' "$line" \
      | sed -E 's/[(#].*$//' \
      | sed -E 's/^[[:space:]]*[-*+][[:space:]]*//' \
      | awk '{print $1}')
  fi
  tok=$(printf '%s\n' "$tok" | sed -E 's/[][`*()"]//g; s/^\.\///; s/[.:;]+$//')
  [ -n "$tok" ] && printf '%s\n' "$tok" >> .ai/build/declared-files.list
done < .ai/build/declared-files.raw

MISSING_DIRS=""
MISSING_FILES=""
CHECKED=0
while IFS= read -r tok; do
  # Path heuristic: keep tokens with a directory separator or a dotted
  # filename; drop prose words. Skip absolute / home-anchored / parent-
  # escaping tokens outright — declared outputs are repo-relative, and
  # these must never re-anchor an existence check outside the workdir.
  case "$tok" in
    ''|/*|~*|*..*) continue ;;
    */*|*.*) ;;
    *) continue ;;
  esac
  CHECKED=$((CHECKED + 1))
  case "$tok" in
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
# structurally absent as a missing directory. Detection mirrors
# ci-probe's go.mod check. Other stacks' build gates already ran in
# TestMilestone and run again in FinalBuild — re-running them here
# would break the sub-second contract for no new signal.
BUILD_FAIL=0
if [ -f go.mod ]; then
  echo "--- go build ./... (structural gate) ---"
  go build ./... 2>&1 || BUILD_FAIL=$?
fi

if [ -n "$MISSING_DIRS" ] || [ "$BUILD_FAIL" -ne 0 ]; then
  if [ -n "$MISSING_DIRS" ]; then
    echo "ERROR: declared milestone output directories are MISSING from disk:"
    for d in $MISSING_DIRS; do echo "  - $d"; done | sort -u
  fi
  if [ "$BUILD_FAIL" -ne 0 ]; then
    echo "ERROR: go build ./... failed (exit $BUILD_FAIL) — see output above"
  fi
  if [ -n "$MISSING_FILES" ]; then
    echo "Declared files also not on disk (these alone would not fail the gate — may be legitimate deletions):"
    for f in $MISSING_FILES; do echo "  - $f"; done
  fi
  echo "A declared milestone output is structurally absent. Escalating BEFORE the review fan-out spends reviewer tokens."
  printf 'outputs-missing'
  exit 1
fi

if [ -n "$MISSING_FILES" ]; then
  echo "WARNING: declared files not on disk (NOT failing — Decompose Files lists include files to delete, and destructive milestones are expected):"
  for f in $MISSING_FILES; do echo "  - $f"; done
  echo "Directory-level structure and the build are intact; a reviewer can disposition these in context."
fi
echo "structural existence gate passed: $CHECKED declared path tokens reconciled"
printf 'outputs-present'