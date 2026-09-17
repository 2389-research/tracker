set -eu
# Shared helpers via the engine-interpolated ${graph.workflow_dir} (author-
# controlled, safe-key allowlisted). Fail loud if empty.
[ -n "${graph.workflow_dir}" ] || { echo "ERROR: graph.workflow_dir is empty — cannot locate build_product's scripts/build_product/lib/ (embedded built-in: engine failed to materialize .tracker/workflow/; packed .dipx: unsupported, see #430)"; exit 1; }
LIB="${graph.workflow_dir}/scripts/build_product/lib"
. "$LIB/gitignore.sh"
# Issue #297: persist green-but-uncommitted work on the Implement SUCCESS
# path so it survives a later node death. Explicit identity makes the
# commit independent of the tool subprocess's git config (unproven here —
# commits otherwise happen inside agent sessions). A genuine commit
# failure is NOT swallowed (no `|| true`); it surfaces per CLAUDE.md
# "never silently swallow errors". The marker is printed last so it
# survives output truncation (CLAUDE.md "Tool output capture").
#
# `git status --porcelain` (not `git diff`) so UNTRACKED files count as
# dirty — a milestone that only adds new files would otherwise look clean
# to `git diff` and skip the commit, leaving exactly the green-but-
# uncommitted tree this node exists to prevent.
# #405: never checkpoint a compiled build artifact. A test that runs
# `go build -o <name>` drops an executable into the tree (Go binaries have
# no fixed extension, so Setup's static .gitignore seed can't catch them);
# `git add -A` would commit it and Verify then FAILs the milestone for
# out-of-scope work. Exclude any UNTRACKED, executable, BINARY, non-empty
# file before staging — real source (text) and executable shell scripts
# (text) survive; only opaque binaries are filtered. The exclusion goes to
# the LOCAL, untracked .git/info/exclude — NOT the tracked .gitignore (PR
# #411): a runtime write to the tracked .gitignore is itself an out-of-scope
# tree change VerifyMilestone would FAIL, defeating the point of skipping
# the artifact. Same .git/info/exclude treatment .tracker/ (Setup) and the
# turn-override dir (ContinueWithMoreTurns) already get. Binary-ness comes
# from git itself: `git diff --no-index --numstat /dev/null <f>` reports `-`
# in the added-lines column for a binary file, a number for text. Using git
# (already required by this node) avoids depending on a GNU-only `grep -I`,
# whose unrecognized-flag failure mode would mis-ignore real source. The
# `[ -s ]` guard keeps an empty placeholder from being mistaken for an
# artifact. Listing comes from git's own untracked-not-excluded set (it
# already honors .git/info/exclude), so nothing already tracked or excluded
# is touched and re-runs are idempotent. Newline-delimited read (POSIX sh —
# tracker runs tool nodes under dash, where `read -d ''` is illegal); git
# quotes any pathname with embedded newlines, so such a name simply fails
# the `[ -x ]` test and is left for `git add -A` rather than mis-excluded.
# The `-- "$f"` end-of-options separator on the diff keeps a leading-dash
# artifact name (e.g. `-weird`) from being parsed as switches (which would
# error, yield empty, miss the `-` case, and leak the binary into `git add
# -A`). The pattern is sed-escaped before being written so gitignore
# metacharacters (`* ? [ ] \ ! #`) in an LLM-influenced artifact name match
# the literal path only, never a broader source set; git_exclude_add
# (lib/gitignore.sh) keeps the append idempotent.
GITDIR=$(git rev-parse --git-dir 2>/dev/null || true)
if [ -n "$GITDIR" ]; then
  git ls-files --others --exclude-standard \
    | while IFS= read -r f; do
        [ -x "$f" ] && [ -s "$f" ] || continue
        case "$(git diff --no-index --numstat /dev/null -- "$f" 2>/dev/null | cut -f1)" in
          -) PAT="/$(printf '%s' "$f" | sed 's/[][*?\\!#]/\\&/g')"
             git_exclude_add "$PAT" ;;
        esac
      done
fi

if [ -n "$(git status --porcelain)" ]; then
  git add -A
  git -c user.name="build_product" -c user.email="build_product@tracker.local" \
    commit -m "chore(milestone): checkpoint working tree (auto-commit by build_product)"
fi
printf 'commit-if-dirty-done'