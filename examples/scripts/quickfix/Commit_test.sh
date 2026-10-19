#!/usr/bin/env bash
# ABOUTME: Fixture tests for quickfix's Commit.sh — commits the worktree's change on the quickfix
# ABOUTME: branch only (secrets/.tracker/ never staged, hooks kept), removes the worktree, keeps the branch.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
. "$DIR/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/Commit.sh")"
PREFLIGHT="$(stage_script "$DIR/Preflight.sh")"

setup() {
  new_repo
  echo dirty >> "$WORK/README.md"
  qf_run "$PREFLIGHT"
  BASE="$(cat "$WORK/.tracker/quickfix/base_sha")"
  USER_STATUS="$(G status --porcelain)"
}

# 1. Approve: the change lands on the branch; the user's side is untouched.
setup
echo "fixed" >> "$WT/README.md"
echo "new" > "$WT/added.txt"
echo 'TOKEN=1' > "$WT/.env"
printf -- '-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n' > "$WT/key.pem"
mkdir -p "$WT/.tracker/quickfix"
printf 'Fixed the README.\n\nIt said `foo`; now it says $(bar).\n' > "$WT/.tracker/quickfix/summary.md"
qf_run "$SCRIPT"
check "commit exits 0"                  "0" "$RC"
check "one commit on the branch"        "1" "$(G rev-list --count "$BASE..$BR")"
FILES="$(G show --name-only --format= "$BR")"
check "edit committed"                  "yes" "$(has_line_in "$FILES" README.md)"
check "new file committed"              "yes" "$(has_line_in "$FILES" added.txt)"
check ".env not committed"              "no" "$(has_line_in "$FILES" .env)"
check "private key not committed"       "no" "$(has_line_in "$FILES" key.pem)"
check ".tracker not committed"          "no" "$(has_re_in "$FILES" '^\.tracker/')"
check "secret skip warned"              "yes" "$(has 'not staging secret-looking')"
check "subject"                         "quickfix: change from tracker run $RUN_ID" "$(G log -1 --format=%s "$BR")"
check "body from summary, verbatim"     "yes" "$(contains "$(G log -1 --format=%b "$BR")" 'It said `foo`; now it says $(bar).')"
check "user branch unmoved"             "$BASE" "$(G rev-parse main)"
check "user still on main"              "main" "$(G branch --show-current)"
check "user tree unchanged"             "$USER_STATUS" "$(G status --porcelain)"
check "worktree removed"                "no" "$(exists "$WT")"
check "branch kept"                     "yes" "$(G show-ref --verify -q "refs/heads/$BR" && echo yes || echo no)"
check "reports the branch"              "yes" "$(has_line "committed to branch $BR (your checkout is unchanged)")"
check "next: diff"                      "yes" "$(has_line "  git diff $BASE..$BR")"
check "next: merge"                     "yes" "$(has_line "  git merge $BR      # or cherry-pick")"
check "next: pr"                        "yes" "$(has_line "  gh pr create --head $BR")"
check "notice for the run summary"      "yes" "$(contains "$(cat "$RUN_DIR/notice.txt" 2>/dev/null)" "git merge $BR")"

# 2. No summary: a fallback body, still committed.
setup
echo "fixed" >> "$WT/README.md"
qf_run "$SCRIPT"
check "no summary: exit 0"              "0" "$RC"
check "no summary: committed"           "1" "$(G rev-list --count "$BASE..$BR")"

# 3. Nothing to commit: fail loudly, keep the worktree.
setup
qf_run "$SCRIPT"
check "empty: exit 1"                   "1" "$RC"
check "empty: says why"                 "yes" "$(has_re_in "$ERR" 'ERROR: .*nothing to commit')"
check "empty: worktree kept"            "yes" "$(exists "$WT")"

# 4. A rejecting pre-commit hook: exit 1, hook output shown, worktree kept.
setup
echo "fixed" >> "$WT/README.md"
mkdir -p "$WORK/.git/hooks"
printf '#!/bin/sh\necho "lint says no" >&2\nexit 1\n' > "$WORK/.git/hooks/pre-commit"
chmod +x "$WORK/.git/hooks/pre-commit"
qf_run "$SCRIPT"
check "hook fail: exit 1"               "1" "$RC"
check "hook fail: output shown"         "yes" "$(contains "$OUT$ERR" 'lint says no')"
check "hook fail: worktree kept"        "yes" "$(exists "$WT")"
check "hook fail: branch unmoved"       "$BASE" "$(G rev-parse "$BR")"
rm -f "$WORK/.git/hooks/pre-commit"

# 5. A rewriting hook (formatter) is folded in with one amend.
setup
echo "fixed" >> "$WT/README.md"
printf '#!/bin/sh\nif ! grep -q formatted README.md; then echo formatted >> README.md; fi\nexit 0\n' > "$WORK/.git/hooks/pre-commit"
chmod +x "$WORK/.git/hooks/pre-commit"
qf_run "$SCRIPT"
check "rewrite hook: exit 0"            "0" "$RC"
check "rewrite hook: folded in"         "yes" "$(contains "$(G show "$BR:README.md")" formatted)"
check "rewrite hook: one commit"        "1" "$(G rev-list --count "$BASE..$BR")"
rm -f "$WORK/.git/hooks/pre-commit"

# 6. commit.gpgsign=true with no key: the commit is made unsigned.
setup
echo "fixed" >> "$WT/README.md"
G config commit.gpgsign true
G config gpg.program /nonexistent-gpg
qf_run "$SCRIPT"
check "gpgsign: exit 0"                 "0" "$RC"
G config --unset commit.gpgsign; G config --unset gpg.program

# 7. No identity configured anywhere: a fixed tracker identity, still committed.
setup
echo "fixed" >> "$WT/README.md"
G config --unset user.name; G config --unset user.email
qf_run "$SCRIPT" GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
check "no identity: exit 0"             "0" "$RC"
check "no identity: fallback author"    "quickfix@tracker.local" "$(G log -1 --format=%ae "$BR")"

# 8. ShowDiff ran first (as it always does before the gate): new secrets,
#    keys and binaries are still caught, and so is a secret the agent staged.
SHOWDIFF="$(stage_script "$DIR/ShowDiff.sh")"
setup
echo "fixed" >> "$WT/README.md"
echo 'TOKEN=1' > "$WT/.env"
printf -- '-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n' > "$WT/key.pem"
printf '\177ELF\000\001binary\000\n' > "$WT/tool"; chmod +x "$WT/tool"
echo 'API=1' > "$WT/.env.local"; git -C "$WT" add .env.local
qf_run "$SHOWDIFF"
qf_run "$SCRIPT"
FILES="$(G show --name-only --format= "$BR")"
check "after ShowDiff: commit exits 0"  "0" "$RC"
check "after ShowDiff: edit committed"  "yes" "$(has_line_in "$FILES" README.md)"
check "after ShowDiff: .env skipped"    "no" "$(has_line_in "$FILES" .env)"
check "after ShowDiff: key skipped"     "no" "$(has_line_in "$FILES" key.pem)"
check "after ShowDiff: binary skipped"  "no" "$(has_line_in "$FILES" tool)"
check "agent-staged secret skipped"     "no" "$(has_line_in "$FILES" .env.local)"

exit "$fail"
