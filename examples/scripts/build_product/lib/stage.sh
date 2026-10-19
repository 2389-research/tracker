# ABOUTME: Shared commit staging rules for build_product's commit nodes (CommitIfDirty,
# ABOUTME: FinalCommit): per-invocation excludes that skip untracked secrets and binaries.
#
# Sourced (never executed) via ${graph.workflow_dir}/scripts/build_product/lib.
# Callers run from the repo toplevel (exclude patterns are root-relative),
# own the temp dir and its cleanup trap, and keep their own commit/amend logic:
#
#   TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
#   stage_build_excludes "$TMP"
#   stage_git status --porcelain; stage_git add -A
#
# #640 C6: every exclusion is PER-INVOCATION — a temp excludes file passed as
# `-c core.excludesFile=` to status/add — never persisted to .git/info/exclude
# and never the tracked .gitignore (PR #411: a runtime .gitignore write is
# itself out-of-scope work Verify FAILs). The old persistent `/server` line
# matched a DIRECTORY forever, so a later server/server.go was never staged
# while the node still printed done. core.excludesFile REPLACES the user's
# global ignore for the command, so that file (explicit config, else the XDG
# default) is copied in first — otherwise `.DS_Store`-style entries would
# start landing in checkpoints.

# stage_git ARGS — git with this invocation's excludes (after
# stage_build_excludes has set STAGE_EXCLUDE).
stage_git() { git -c core.excludesFile="$STAGE_EXCLUDE" "$@"; }

# stage_build_excludes TMPDIR — write TMPDIR/exclude (the user's global
# excludes + secret-name rules + private-key files + compiled-binary
# artifacts), set STAGE_EXCLUDE to it, and print the skipped-secret warning
# and one line per skipped binary.
stage_build_excludes() {
  STAGE_EXCLUDE="$1/exclude"
  _stage_user_excl=$(git config --path --get core.excludesFile 2>/dev/null || true)
  [ -n "$_stage_user_excl" ] || _stage_user_excl="${XDG_CONFIG_HOME:-${HOME:-}/.config}/git/ignore"
  if [ -f "$_stage_user_excl" ]; then cat "$_stage_user_excl" > "$STAGE_EXCLUDE"; else : > "$STAGE_EXCLUDE"; fi
  _stage_secrets "$1/secrets"
  _stage_binaries
}

# #640 C5 (PARTIAL — the commit nodes' half): `git add -A` sweeps the whole
# tree, and an operator's untracked `.env` / private key next to the product
# has been committed into a CHECKPOINT commit that way. Untracked
# secret-looking files are never staged — loudly, so the operator knows they
# were left out (they stay on disk, untracked, and remain visible to later
# nodes' `git status`). Name-based: `.env`, `.env.*` (minus the documentation
# `.env.example`/`.env.sample`), `id_rsa*`, `*.p12`. Content-based for
# `*.pem`/`*.key`: a public cert/key (testdata/cert.pem, keys/pub.key) is
# plausible TLS-milestone source and MUST ship, so only a file containing a
# `-----BEGIN ... PRIVATE KEY-----` block is skipped. Escape hatch for a
# deliberate exception: `!<path>` in .gitignore — a negation there outranks
# this excludes file (core.excludesFile is the lowest-precedence source).
# Only UNTRACKED files are affected (ignore rules never touch tracked
# paths). The user-WIP half — a dirty-tree preflight at Setup — is NOT
# handled here.
_stage_secrets() {
  printf '%s\n' '.env' '.env.*' '!.env.example' '!.env.sample' \
    'id_rsa*' '*.p12' > "$1"
  git ls-files --others --exclude-standard \
    | while IFS= read -r f; do
        case "$f" in *.pem|*.key) ;; *) continue ;; esac
        if grep -q -- '-----BEGIN .*PRIVATE KEY-----' "$f" 2>/dev/null; then
          printf '/%s\n' "$(printf '%s' "$f" | sed 's/[][*?\\!#]/\\&/g')" >> "$1"
        fi
      done
  # Candidates are already untracked-and-not-ignored, so anything
  # check-ignore flags with the secrets list as the excludes file matched that
  # list. check-ignore also reports paths hit by a `!negation`, so `-v`
  # (source:line:pattern<TAB>path) lets those be dropped — `.env.example`
  # still ships.
  _stage_skipped=$(git ls-files --others --exclude-standard \
    | git -c core.excludesFile="$1" check-ignore -v --stdin 2>/dev/null \
    | awk -F'\t' 'index($1, ":!") == 0 { print $2 }' || true)
  if [ -n "$_stage_skipped" ]; then
    echo "WARNING: not staging secret-looking untracked file(s) (left on disk, untracked; to ship one deliberately, add \`!<path>\` to .gitignore):"
    printf '%s\n' "$_stage_skipped" | sed 's/^/  /'
  fi
  cat "$1" >> "$STAGE_EXCLUDE"
}

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
_stage_binaries() {
  git ls-files --others --exclude-standard \
    | while IFS= read -r f; do
        [ -x "$f" ] && [ -s "$f" ] || continue
        case "$(git diff --no-index --numstat /dev/null -- "$f" 2>/dev/null | cut -f1)" in
          -) printf '/%s\n' "$(printf '%s' "$f" | sed 's/[][*?\\!#]/\\&/g')" >> "$STAGE_EXCLUDE"
             echo "skipping compiled binary artifact: $f" ;;
        esac
      done
}
