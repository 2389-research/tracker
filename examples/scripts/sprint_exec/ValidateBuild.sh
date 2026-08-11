set -eu
if [ -f go.mod ]; then
  go build ./... >/tmp/sprint-exec-build.log 2>&1 || { cat /tmp/sprint-exec-build.log; exit 1; }
  go test ./... >/tmp/sprint-exec-test.log 2>&1 || { cat /tmp/sprint-exec-test.log; exit 1; }
  cat /tmp/sprint-exec-test.log
  printf 'validation-pass-go'
  exit 0
fi
if [ -f Package.swift ]; then
  swift build >/tmp/sprint-exec-build.log 2>&1
  swift test >/tmp/sprint-exec-test.log 2>&1 || true
  cat /tmp/sprint-exec-build.log
  cat /tmp/sprint-exec-test.log
  if rg -n 'error:' /tmp/sprint-exec-build.log >/dev/null 2>&1; then exit 1; fi
  if rg -n 'failed' /tmp/sprint-exec-test.log >/dev/null 2>&1; then exit 1; fi
  printf 'validation-pass-swift'
  exit 0
fi
if [ -f package.json ]; then
  npm test >/tmp/sprint-exec-test.log 2>&1 || { cat /tmp/sprint-exec-test.log; exit 1; }
  printf 'validation-pass-node'
  exit 0
fi
printf 'validation-pass-no-known-build-system'