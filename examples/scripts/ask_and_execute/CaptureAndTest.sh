set -eu
MAIN=$(git rev-parse --abbrev-ref HEAD)
RESULTS=""
for NAME in claude codex gemini; do
  WTDIR=".ai/worktrees/$NAME"
  BRANCH="impl/$NAME"
  DIFF_FILE=".ai/candidates/$NAME.diff"
  TEST_FILE=".ai/candidates/$NAME.test"

  # Capture the diff against main
  git -C "$WTDIR" diff "$MAIN"..HEAD > "$DIFF_FILE" 2>/dev/null || true

  # Run tests in the worktree
  cd "$WTDIR"
  if [ -f go.mod ]; then
    go build ./... > "$OLDPWD/.ai/candidates/$NAME.test" 2>&1 && \
    go test ./... >> "$OLDPWD/.ai/candidates/$NAME.test" 2>&1
    TEST_EXIT=$?
  elif [ -f package.json ]; then
    npm test > "$OLDPWD/.ai/candidates/$NAME.test" 2>&1
    TEST_EXIT=$?
  else
    echo "no known build system" > "$OLDPWD/.ai/candidates/$NAME.test"
    TEST_EXIT=0
  fi
  cd "$OLDPWD"

  DIFF_LINES=$(wc -l < "$DIFF_FILE" | tr -d ' ')
  if [ $TEST_EXIT -eq 0 ]; then
    RESULTS="$RESULTS\n$NAME: TESTS PASS, diff=$DIFF_LINES lines"
  else
    RESULTS="$RESULTS\n$NAME: TESTS FAIL (exit=$TEST_EXIT), diff=$DIFF_LINES lines"
  fi
done
# %b expands the \n escapes in RESULTS while keeping it data, not a
# format string — test names containing % can't break the printf.
printf '%b' "$RESULTS"