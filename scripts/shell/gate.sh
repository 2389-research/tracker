#!/usr/bin/env bash
# ABOUTME: Shell-hygiene gate — flags `producer | <early-exiting consumer>` in
# ABOUTME: scripts that enable pipefail (the SIGPIPE false-negative class of #658).
set -euo pipefail
export LC_ALL=C

# The one thing we can check without false positives:
#   pipe-consumers   no pipefail script pipes into grep -q/-l/-L/-m, head,
#                    `sed … q`, `read` or `cmp -s`
#
# Why: under `set -o pipefail` a pipeline's status is the LAST non-zero
# status. bash's builtin printf flushes at every newline, so `printf '%s'
# "$OUT" | grep -q NEEDLE` writes one line per write(2); grep -q exits on its
# first match, the next write hits a closed pipe (SIGPIPE, 141) and pipefail
# reports 141 — `… && echo yes || echo no` prints "no" for text that IS
# present. Below pipe capacity the race is load dependent (5/720 suite runs at
# 12-way parallelism in the #658 triage); it is never zero. The fix is a
# consumer that drains its input (`grep … >/dev/null` without -q, `tail`,
# `sed -n 1,20p`, `case` on the variable — see test_helpers.sh), a FILE
# operand (`grep -q PAT FILE` has no producer), or a here-string.
#
# Escapes: a line that ends in `|| true` (status deliberately ignored) or
# carries `# pipefail-ok: <reason>` (reason REQUIRED) is skipped; comment
# lines are skipped. Zero files examined is FATAL so a broken glob cannot pass.

# Scan roots (space-separated) and the directories pruned under them.
SHELL_GATE_ROOTS="${SHELL_GATE_ROOTS:-.}"
SHELL_GATE_PRUNE="${SHELL_GATE_PRUNE:-.git .claude .worktrees .scratch .gocache node_modules bin site/public}"

# A pipe (not `||`) followed by an early-exiting consumer. grep: any single-dash
# cluster containing q/l/L/m (after any other flags), or the long spellings.
# `| while read` is NOT flagged (the loop drains); `| read` / `| IFS= read` is.
CONSUMER_RE='(^|[^|])\|[[:space:]]*('
CONSUMER_RE+='grep([[:space:]]+-[-A-Za-z=0-9]+)*[[:space:]]+(-[A-Za-z]*[qlLm][A-Za-z0-9]*|--quiet|--silent|--max-count)'
CONSUMER_RE+='|head([[:space:]]|$)'
CONSUMER_RE+="|sed[[:space:]]+[^|]*q['\"]?[;}]*([[:space:]]|\$)"
CONSUMER_RE+='|(IFS=[^[:space:]]*[[:space:]]+)?read([[:space:]]|$)'
CONSUMER_RE+='|cmp[[:space:]]+-s'
CONSUMER_RE+=')'
# `set -euo pipefail`, `set -o pipefail`, `set -e -o pipefail`, `set -o errexit -o pipefail`.
PIPEFAIL_RE='set[[:space:]].*-[a-zA-Z]*o[[:space:]]+pipefail'

list_shell_files() {
  local root prune_args=() p
  for p in $SHELL_GATE_PRUNE; do prune_args+=(-o -path "*/$p" -o -path "*/$p/*"); done
  for root in $SHELL_GATE_ROOTS; do
    [ -e "$root" ] || continue
    # shellcheck disable=SC2016  # the parens are find's, not shell groups
    find "$root" \( -false "${prune_args[@]}" \) -prune -o -type f \( -name '*.sh' -o -name '*.bash' -o -name '.pre-commit' \) -print
  done | sort -u
}

pipe_consumers() {
  local files=0 pipefail_files=0 hits=0 f line n joined start pending
  while IFS= read -r f; do
    files=$((files+1))
    grep -qE "$PIPEFAIL_RE" "$f" || continue
    pipefail_files=$((pipefail_files+1))
    n=0; joined=''; start=0; pending=0
    # Fold shell logical lines before matching: a physical line that ends in a
    # single `|` or a `\` continuation carries its consumer onto the next line,
    # so testing each physical line alone would miss `producer |⏎<consumer>`
    # (the #658 split-pipeline false negative, #664). Join such lines first,
    # then run CONSUMER_RE / the escapes on the complete command.
    while IFS= read -r line; do
      n=$((n+1))
      if [ "$pending" -eq 0 ]; then
        [[ $line =~ ^[[:space:]]*(#|$) ]] && continue   # whole-line comment / blank
        start=$n; joined=$line
      else
        joined="$joined $line"
      fi
      if [[ $line =~ \\$ ]]; then joined="${joined%\\}"; pending=1; continue; fi   # `\`-continuation
      if [[ $line =~ (^|[^|])\|[[:space:]]*(\#.*)?$ ]]; then pending=1; continue; fi  # pipe at EOL
      pending=0
      [[ $joined =~ $CONSUMER_RE ]] || continue
      if [[ $joined =~ \#[[:space:]]*pipefail-ok ]]; then
        if [[ $joined =~ \#[[:space:]]*pipefail-ok:[[:space:]]*[^[:space:]] ]]; then continue; fi
        echo "  NOREASON $f:$start: '# pipefail-ok' needs a reason — '# pipefail-ok: <why this consumer drains / status is unused>'"
        hits=$((hits+1)); continue
      fi
      [[ $joined =~ \|\|[[:space:]]+true[[:space:]]*(\;)?[[:space:]]*$ ]] && continue
      echo "  PIPE     $f:$start: ${joined#"${joined%%[![:space:]]*}"}"
      hits=$((hits+1))
    done < "$f"
  done < <(list_shell_files)
  if [ "$files" -eq 0 ]; then
    echo "FATAL: shell gate examined zero files under '$SHELL_GATE_ROOTS' — refusing to pass (glob/roots drifted?)" >&2
    exit 1
  fi
  if [ "$hits" -ne 0 ]; then
    cat >&2 <<EOF
FAIL: $hits pipe(s) into an early-exiting consumer under pipefail (SIGPIPE → 141 → false negative; #658).
      Use the pipe-free helpers in examples/scripts/*/test_helpers.sh (has / has_line / has_re /
      contains / first_line), a FILE operand, a here-string, or a draining consumer; append
      '|| true' when the status is genuinely unused, or '# pipefail-ok: <reason>' to document why
      this one is safe.
EOF
    exit 1
  fi
  echo "shell gate OK: $pipefail_files pipefail script(s) of $files examined pipe into no early-exiting consumer"
}

cmd="${1:-}"
case "$cmd" in
  pipe-consumers) pipe_consumers ;;
  *) echo "usage: gate.sh pipe-consumers   (env: SHELL_GATE_ROOTS='dir …', SHELL_GATE_PRUNE='name …')" >&2; exit 2 ;;
esac
