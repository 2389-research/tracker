set -eu
# Issue #656: the FINAL commit of a build_product run. Replaced the former
# auto_status *agent* node (an LLM at reasoning_effort:low whose mandatory
# early `STATUS:fail`, unoverridden on a clean tree or by a truncated reply,
# made a GOOD build fail — then --auto-approve's EscalateReview→Cleanup→
# FinalCommit fallback loop spent the one-shot latch and dead-stopped the run
# with "no conditional edges to handle failure"). A deterministic tool node
# cannot mis-report a clean tree, and — being a fixed script — cannot author
# unreviewed product source, which is exactly what the old node's #349/#272
# writable_paths jail existed to prevent, so the jail is now moot.
#
# Contract (routed by the .dip on ctx.outcome):
#   exit 0  → success: a clean/already-committed tree (reports HEAD), or the
#             final sweep committed leftover work. Prints `final-commit-done`
#             LAST so the marker survives output truncation.
#   exit 1  → genuine failure (routed to AbortRun, run ends `fail`, WIP on
#             disk preserved): not a git repo, a clean tree with NO HEAD
#             (nothing was ever committed the whole run — no build happened),
#             or a commit rejected by a hook. Nothing is swallowed (no
#             `|| true` on the commit) per CLAUDE.md "never silently swallow
#             errors: a commit that should have happened but didn't must fail".
#
# The staging idiom (per-invocation secret/binary exclusion, unsigned commit,
# hooks kept, amend-once) mirrors CommitIfDirty.sh; kept self-contained rather
# than shared so this P1 fix does not touch the working checkpoint node. A
# follow-up may extract both into lib/ (ties #395/#646).

git rev-parse --is-inside-work-tree >/dev/null 2>&1 \
  || { echo "ERROR: FinalCommit: not a git repository: $(pwd) — build_product requires the workdir to be a git repo"; exit 1; }
cd "$(git rev-parse --show-toplevel)"

# A run that reaches FinalCommit with NO commit at all never built anything:
# every milestone's CommitIfDirty and the review-fix commit would have created
# history. A clean tree with no HEAD is therefore a genuine failure, not a
# no-op success — fail loud so it routes to AbortRun instead of shipping a
# success marker over an empty repo.
if ! git rev-parse --verify -q HEAD >/dev/null 2>&1; then
  echo "ERROR: FinalCommit: repository has no commits (HEAD unborn) — no build work was ever committed; nothing to finalize"
  exit 1
fi

# Per-invocation excludes: core.excludesFile REPLACES the user's global ignore
# for the command, so copy the real one (explicit config, else the XDG default)
# in first, then append this node's own secret/binary rules. Never written to
# .gitignore / info/exclude (a runtime .gitignore write is out-of-scope work a
# later Verify would FAIL). Mirrors CommitIfDirty.sh #640 C5/C6/#405.
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
EXCL="$TMP/exclude"
USER_EXCL=$(git config --path --get core.excludesFile 2>/dev/null || true)
[ -n "$USER_EXCL" ] || USER_EXCL="${XDG_CONFIG_HOME:-${HOME:-}/.config}/git/ignore"
if [ -f "$USER_EXCL" ]; then cat "$USER_EXCL" > "$EXCL"; else : > "$EXCL"; fi
g() { git -c core.excludesFile="$EXCL" "$@"; }

# Never stage an operator's untracked secret in the final `git add -A` sweep.
# Name-based (.env / id_rsa* / *.p12, keeping .env.example/.sample), plus a
# content check for *.pem/*.key so a public cert still ships but a file with a
# `-----BEGIN ... PRIVATE KEY-----` block is skipped. `!<path>` in .gitignore
# overrides (core.excludesFile is lowest precedence). Only UNTRACKED files are
# affected. Byte-for-byte the CommitIfDirty.sh rule.
SECRETS="$TMP/secrets"
printf '%s\n' '.env' '.env.*' '!.env.example' '!.env.sample' \
  'id_rsa*' '*.p12' > "$SECRETS"
git ls-files --others --exclude-standard \
  | while IFS= read -r f; do
      case "$f" in *.pem|*.key) ;; *) continue ;; esac
      if grep -q -- '-----BEGIN .*PRIVATE KEY-----' "$f" 2>/dev/null; then
        printf '/%s\n' "$(printf '%s' "$f" | sed 's/[][*?\\!#]/\\&/g')" >> "$SECRETS"
      fi
    done
SKIPPED=$(git ls-files --others --exclude-standard \
  | git -c core.excludesFile="$SECRETS" check-ignore -v --stdin 2>/dev/null \
  | awk -F'\t' 'index($1, ":!") == 0 { print $2 }' || true)
if [ -n "$SKIPPED" ]; then
  echo "WARNING: not staging secret-looking untracked file(s) (left on disk, untracked; to ship one deliberately, add \`!<path>\` to .gitignore):"
  printf '%s\n' "$SKIPPED" | sed 's/^/  /'
fi
cat "$SECRETS" >> "$EXCL"

# Never checkpoint a compiled binary artifact a test dropped in the tree
# (#405). Untracked + executable + git-classified-binary + non-empty only;
# real source (text) and shell scripts (text) survive. Byte-for-byte the
# CommitIfDirty.sh rule.
git ls-files --others --exclude-standard \
  | while IFS= read -r f; do
      [ -x "$f" ] && [ -s "$f" ] || continue
      case "$(git diff --no-index --numstat /dev/null -- "$f" 2>/dev/null | cut -f1)" in
        -) printf '/%s\n' "$(printf '%s' "$f" | sed 's/[][*?\\!#]/\\&/g')" >> "$EXCL"
           echo "skipping compiled binary artifact: $f" ;;
      esac
    done

report_head() { echo "FinalCommit HEAD: $(git rev-parse HEAD)"; printf 'final-commit-done'; }

# Clean / already-committed tree is the NORMAL, SUCCESSFUL final state: every
# milestone's CommitIfDirty and ApplyReviewFixes already committed the work.
# Report HEAD and the marker; make NO empty commit.
if [ -z "$(g status --porcelain)" ]; then
  report_head
  exit 0
fi

# Leftover work exists — sweep it into one final commit. Unsigned (#640 C7:
# tracker's checkpoints, not the operator's signed history). Hooks KEPT (no
# --no-verify — CLAUDE.md forbids it): a hook failure exits 1 loudly with no
# marker, and the run routes to AbortRun instead of finalizing over a broken
# tree.
g add -A
COMMIT_OUT="$TMP/commit.out"
commit() {
  git -c user.name="build_product" -c user.email="build_product@tracker.local" \
      -c commit.gpgsign=false commit "$@" >"$COMMIT_OUT" 2>&1
}
rc=0
commit -m "chore: final commit of reviewed build (auto-commit by build_product)" || rc=$?
cat "$COMMIT_OUT"
if [ "$rc" -ne 0 ]; then
  echo "ERROR: final commit failed (git exit $rc) — see the hook/git output above; fix the hook or the tree, then resume"
  exit 1
fi
# A rewriting pre-commit hook (formatter) leaves ` M` after a successful
# commit; fold it in with one amend (an idempotent formatter converges). A
# tree still dirty after that is a non-converging hook — fail loud.
if [ -n "$(g status --porcelain)" ]; then
  echo "pre-commit hook rewrote files — folding them into the final commit (amend once)"
  g add -A
  rc=0
  commit --amend --no-edit || rc=$?
  cat "$COMMIT_OUT"
  if [ "$rc" -ne 0 ]; then
    echo "ERROR: final amend failed (git exit $rc) — see the hook/git output above"
    exit 1
  fi
  if [ -n "$(g status --porcelain)" ]; then
    echo "ERROR: working tree still dirty after the final commit — a pre-commit hook rewrites files on every commit:"
    g status --porcelain
    exit 1
  fi
fi
report_head
