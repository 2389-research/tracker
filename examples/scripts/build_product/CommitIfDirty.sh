set -eu
# Issue #297: persist green-but-uncommitted work on the Implement SUCCESS
# path so it survives a later node death. Explicit identity makes the
# commit independent of the tool subprocess's git config (unproven here —
# commits otherwise happen inside agent sessions). A genuine commit
# failure is NOT swallowed (no `|| true`); it surfaces per CLAUDE.md
# "never silently swallow errors". The marker is printed last so it
# survives output truncation (CLAUDE.md "Tool output capture").
#
# #640 E7: outside a git repo this is an invariant violation (build_product
# `requires: git`; every checkpoint depends on a repo), so fail LOUD instead
# of reading a failed `git status` as "clean" and shipping the marker.
git rev-parse --is-inside-work-tree >/dev/null 2>&1 \
  || { echo "ERROR: CommitIfDirty: not a git repository: $(pwd) — build_product checkpoints require the workdir to be a git repo (\`git init\` it, or run from the repo root)"; exit 1; }
# Everything below is whole-tree (status/add/commit), and exclude patterns in
# an excludes file are root-relative, so anchor at the toplevel once.
cd "$(git rev-parse --show-toplevel)"

# `git status --porcelain` (not `git diff`) so UNTRACKED files count as
# dirty — a milestone that only adds new files would otherwise look clean
# to `git diff` and skip the commit, leaving exactly the green-but-
# uncommitted tree this node exists to prevent.
#
# #640 C6: every exclusion this node computes is PER-INVOCATION — a temp
# excludes file passed as `-c core.excludesFile=` to status/add — never
# persisted to .git/info/exclude and never the tracked .gitignore (PR #411:
# a runtime .gitignore write is itself out-of-scope work Verify FAILs). The
# old persistent `/server` line matched a DIRECTORY forever, so a later
# server/server.go was never staged while the node still printed done.
# core.excludesFile REPLACES the user's global ignore for the command, so
# that file (explicit config, else the XDG default) is copied in first —
# otherwise `.DS_Store`-style entries would start landing in checkpoints.
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
EXCL="$TMP/exclude"
USER_EXCL=$(git config --path --get core.excludesFile 2>/dev/null || true)
[ -n "$USER_EXCL" ] || USER_EXCL="${XDG_CONFIG_HOME:-${HOME:-}/.config}/git/ignore"
if [ -f "$USER_EXCL" ]; then cat "$USER_EXCL" > "$EXCL"; else : > "$EXCL"; fi
g() { git -c core.excludesFile="$EXCL" "$@"; }

# #640 C5 (PARTIAL — this node's half): `git add -A` sweeps the whole tree,
# and an operator's untracked `.env` / private key next to the product has
# been committed into a CHECKPOINT commit that way. Untracked secret-looking
# files are never staged here — loudly, so the operator knows they were left
# out (they stay on disk, untracked, and remain visible to later nodes'
# `git status`). Name-based: `.env`, `.env.*` (minus the documentation
# `.env.example`/`.env.sample`), `id_rsa*`, `*.p12`. Content-based for
# `*.pem`/`*.key`: a public cert/key (testdata/cert.pem, keys/pub.key) is
# plausible TLS-milestone source and MUST ship, so only a file containing a
# `-----BEGIN ... PRIVATE KEY-----` block is skipped. Escape hatch for a
# deliberate exception: `!<path>` in .gitignore — a negation there outranks
# this excludes file (core.excludesFile is the lowest-precedence source).
# Only UNTRACKED files are affected (ignore rules never touch tracked
# paths). The user-WIP half — a dirty-tree preflight at Setup — is NOT
# handled here.
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
# Candidates are already untracked-and-not-ignored, so anything check-ignore
# flags with the secrets list as the excludes file matched that list.
# check-ignore also reports paths hit by a `!negation`, so `-v` (source:line:
# pattern<TAB>path) lets those be dropped — `.env.example` still ships.
SKIPPED=$(git ls-files --others --exclude-standard \
  | git -c core.excludesFile="$SECRETS" check-ignore -v --stdin 2>/dev/null \
  | awk -F'\t' 'index($1, ":!") == 0 { print $2 }' || true)
if [ -n "$SKIPPED" ]; then
  echo "WARNING: not staging secret-looking untracked file(s) (left on disk, untracked; to ship one deliberately, add \`!<path>\` to .gitignore):"
  printf '%s\n' "$SKIPPED" | sed 's/^/  /'
fi
cat "$SECRETS" >> "$EXCL"

# #405: never checkpoint a compiled build artifact. A test that runs
# `go build -o <name>` drops an executable into the tree (Go binaries have
# no fixed extension, so Setup's static .gitignore seed can't catch them);
# `git add -A` would commit it and Verify then FAILs the milestone for
# out-of-scope work. Exclude any UNTRACKED, executable, BINARY, non-empty
# file before staging — real source (text) and executable shell scripts
# (text) survive; only opaque binaries are filtered. Binary-ness comes
# from git itself: `git diff --no-index --numstat /dev/null <f>` reports `-`
# in the added-lines column for a binary file, a number for text. Using git
# (already required by this node) avoids depending on a GNU-only `grep -I`,
# whose unrecognized-flag failure mode would mis-ignore real source. The
# `[ -s ]` guard keeps an empty placeholder from being mistaken for an
# artifact. Listing comes from git's own untracked-not-excluded set (it
# already honors .gitignore + info/exclude), so nothing already tracked or
# excluded is touched and re-runs are idempotent. Newline-delimited read
# (POSIX sh — tracker runs tool nodes under dash, where `read -d ''` is
# illegal); git quotes any pathname with embedded newlines, so such a name
# simply fails the `[ -x ]` test and is left for `git add -A` rather than
# mis-excluded. The `-- "$f"` end-of-options separator on the diff keeps a
# leading-dash artifact name (e.g. `-weird`) from being parsed as switches
# (which would error, yield empty, miss the `-` case, and leak the binary
# into `git add -A`). The pattern is sed-escaped and root-anchored so
# gitignore metacharacters (`* ? [ ] \ ! #`) in an LLM-influenced artifact
# name match the literal path only, never a broader source set.
git ls-files --others --exclude-standard \
  | while IFS= read -r f; do
      [ -x "$f" ] && [ -s "$f" ] || continue
      case "$(git diff --no-index --numstat /dev/null -- "$f" 2>/dev/null | cut -f1)" in
        -) printf '/%s\n' "$(printf '%s' "$f" | sed 's/[][*?\\!#]/\\&/g')" >> "$EXCL"
           echo "skipping compiled binary artifact: $f" ;;
      esac
    done

if [ -z "$(g status --porcelain)" ]; then
  printf 'commit-if-dirty-done'
  exit 0
fi
g add -A

# #640 C7: these checkpoints are tracker's, not the user's signed history —
# `commit.gpgsign=true` with no usable key would otherwise fail every
# checkpoint with exit 128, so the commit is made unsigned. The user's hooks
# are KEPT (no --no-verify — CLAUDE.md forbids it, and a failing hook is a
# real finding): git sends hook output to stderr, so it is captured and
# echoed on stdout either way, and a hook failure exits 1 loudly with no
# marker (the run then escalates instead of shipping over a broken tree).
COMMIT_OUT="$TMP/commit.out"
commit() {
  git -c user.name="build_product" -c user.email="build_product@tracker.local" \
      -c commit.gpgsign=false commit "$@" >"$COMMIT_OUT" 2>&1
}
rc=0
commit -m "chore(milestone): checkpoint working tree (auto-commit by build_product)" || rc=$?
cat "$COMMIT_OUT"
if [ "$rc" -ne 0 ]; then
  echo "ERROR: checkpoint commit failed (git exit $rc) — see the hook/git output above; fix the hook or the tree, then resume"
  exit 1
fi
# A pre-commit hook that REWRITES files (formatter) leaves ` M` behind after
# a successful commit. Fold the rewrite into the checkpoint with ONE amend
# (the hook runs again on the amend; an idempotent formatter converges);
# a tree still dirty after that means a non-converging hook — fail loud
# rather than print done over a dirty tree.
if [ -n "$(g status --porcelain)" ]; then
  echo "pre-commit hook rewrote files — folding them into the checkpoint (amend once)"
  g add -A
  rc=0
  commit --amend --no-edit || rc=$?
  cat "$COMMIT_OUT"
  if [ "$rc" -ne 0 ]; then
    echo "ERROR: checkpoint amend failed (git exit $rc) — see the hook/git output above"
    exit 1
  fi
  if [ -n "$(g status --porcelain)" ]; then
    echo "ERROR: working tree still dirty after the checkpoint — a pre-commit hook rewrites files on every commit:"
    g status --porcelain
    exit 1
  fi
fi
printf 'commit-if-dirty-done'
