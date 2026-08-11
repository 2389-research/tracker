set -eu
echo '=== Final verification before ship ==='
if [ -f pyproject.toml ]; then
  uv run pytest -v 2>&1 || { echo 'FINAL_TEST_FAIL'; exit 1; }
elif [ -f package.json ]; then
  npm test 2>&1 || { echo 'FINAL_TEST_FAIL'; exit 1; }
elif [ -f go.mod ]; then
  go test ./... 2>&1 || { echo 'FINAL_TEST_FAIL'; exit 1; }
elif [ -f Cargo.toml ]; then
  cargo test 2>&1 || { echo 'FINAL_TEST_FAIL'; exit 1; }
fi
printf 'final-verification-pass'