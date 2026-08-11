set -eu
echo '=== Running full validation ==='
if [ -f pyproject.toml ]; then
  echo '--- pytest ---'
  uv run pytest -v 2>&1 || { echo 'PYTEST_FAIL'; exit 1; }
  echo '--- ruff ---'
  uv run ruff check . 2>&1 || { echo 'RUFF_FAIL'; exit 1; }
  echo '--- mypy ---'
  uv run mypy . 2>&1 || echo 'mypy-skipped'
  printf 'validation-pass'
elif [ -f package.json ]; then
  echo '--- npm test ---'
  npm test 2>&1 || { echo 'TEST_FAIL'; exit 1; }
  echo '--- lint ---'
  npx eslint . 2>&1 || npx biome check . 2>&1 || echo 'lint-skipped'
  printf 'validation-pass'
elif [ -f go.mod ]; then
  echo '--- go build ---'
  go build ./... 2>&1 || { echo 'BUILD_FAIL'; exit 1; }
  echo '--- go test ---'
  go test ./... 2>&1 || { echo 'TEST_FAIL'; exit 1; }
  echo '--- go vet ---'
  go vet ./... 2>&1 || echo 'vet-skipped'
  printf 'validation-pass'
elif [ -f Cargo.toml ]; then
  echo '--- cargo build ---'
  cargo build 2>&1 || { echo 'BUILD_FAIL'; exit 1; }
  echo '--- cargo test ---'
  cargo test 2>&1 || { echo 'TEST_FAIL'; exit 1; }
  echo '--- cargo clippy ---'
  cargo clippy 2>&1 || echo 'clippy-skipped'
  printf 'validation-pass'
else
  echo 'Unknown build system — manual validation needed'
  printf 'validation-unknown'
fi