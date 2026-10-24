#!/usr/bin/env bats
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Tests for the DDEV add-on, after DDEV's add-on template. CI runs them with
# ddev/github-action-add-on-test; by hand (needs DDEV and bats):
#   bats integrations/ddev/tests --filter-tags '!release'
# ECHO_TEST_IMAGE runs another image than the published one, such as one
# built from this checkout (see the ddev job in .github/workflows/ci.yml).

setup() {
  set -eu -o pipefail

  export GITHUB_REPO=RhythmShahriar/echo

  TEST_BREW_PREFIX="$(brew --prefix 2>/dev/null || true)"
  export BATS_LIB_PATH="${BATS_LIB_PATH}:${TEST_BREW_PREFIX}/lib:/usr/lib/bats"
  bats_load_library bats-assert
  bats_load_library bats-file
  bats_load_library bats-support

  export DIR="$(cd "$(dirname "${BATS_TEST_FILENAME}")/.." >/dev/null 2>&1 && pwd)"
  export PROJNAME="test-ddev-echo"
  mkdir -p "${HOME}/tmp"
  export TESTDIR="$(mktemp -d "${HOME}/tmp/${PROJNAME}.XXXXXX")"
  export DDEV_NONINTERACTIVE=true
  export DDEV_NO_INSTRUMENTATION=true
  export CI=true
  ddev delete -Oy "${PROJNAME}" >/dev/null 2>&1 || true
  cd "${TESTDIR}"
  run ddev config --project-name="${PROJNAME}" --project-tld=ddev.site
  assert_success
  run ddev start -y
  assert_success
}

# use_test_image points the add-on at ECHO_TEST_IMAGE when it is set.
use_test_image() {
  if [ -n "${ECHO_TEST_IMAGE:-}" ]; then
    run ddev dotenv set .ddev/.env.echo --echo-docker-image="${ECHO_TEST_IMAGE}"
    assert_success
  fi
}

# wait_for_mail polls Echo's Mailpit-style API until a message with
# subject $1 is there.
wait_for_mail() {
  for _ in $(seq 1 30); do
    if ddev exec curl -sf http://echo:8025/api/v1/messages | grep -q "\"Subject\":\"$1\""; then
      return 0
    fi
    sleep 1
  done
  ddev exec curl -s http://echo:8025/api/v1/messages >&3 || true
  return 1
}

health_checks() {
  # Echo answers inside the project network and through the router on
  # the port Mailpit had.
  run ddev exec curl -sf http://echo:8025/v1/health
  assert_success
  assert_output --partial '"tool":"rhythm-echo"'
  run curl -sfk "https://${PROJNAME}.ddev.site:8026/v1/health"
  assert_success
  assert_output --partial '"tool":"rhythm-echo"'

  # PHP's mail() goes to DDEV's Mailpit, which relays it to Echo.
  echo '<?php exit(mail("ann@example.com", "Hello from DDEV", "It works.") ? 0 : 1);' >"${TESTDIR}/echo-mail.php"
  run ddev exec php echo-mail.php
  assert_success
  run wait_for_mail "Hello from DDEV"
  assert_success

  # The add-on's command opens the same URL.
  assert_file_exist "${TESTDIR}/.ddev/commands/host/echo"
}

teardown() {
  set -eu -o pipefail
  ddev delete -Oy "${PROJNAME}" >/dev/null 2>&1
  # Persist TESTDIR when running on GitHub Actions, for the artifacts.
  if [ -n "${GITHUB_ENV:-}" ] && [ -f "${GITHUB_ENV}" ]; then
    echo "TESTDIR=${HOME}/tmp/${PROJNAME}" >>"${GITHUB_ENV}"
  else
    [ "${TESTDIR}" != "" ] && rm -rf "${TESTDIR}"
  fi
}

@test "install from directory" {
  set -eu -o pipefail
  echo "# ddev add-on get ${DIR} with project ${PROJNAME} in $(pwd)" >&3
  run ddev add-on get "${DIR}"
  assert_success
  use_test_image
  run ddev restart -y
  assert_success
  health_checks
}

# bats test_tags=release
@test "install from release" {
  set -eu -o pipefail
  url="https://github.com/${GITHUB_REPO}/releases/latest/download/ddev-echo.tar.gz"
  echo "# ddev add-on get ${url} with project ${PROJNAME} in $(pwd)" >&3
  run ddev add-on get "${url}"
  assert_success
  run ddev restart -y
  assert_success
  health_checks
}
