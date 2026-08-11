set -eu
swift build --target $target_name >/tmp/semport-thematic-$target_name-build.log 2>&1 || {
  printf 'BUILD_FAIL\n'
  grep 'error:' /tmp/semport-thematic-$target_name-build.log | head -80 || true
  exit 1
}
printf 'BUILD_PASS'