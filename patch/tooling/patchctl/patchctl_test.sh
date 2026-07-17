#!/usr/bin/env bash

set -Eeuo pipefail

REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
PATCHCTL_SOURCE="${REPOSITORY_ROOT}/patch/2026-07-16/patchctl.sh"
TEST_BASE="${REPOSITORY_ROOT}/.cache/patchctl-tests"
mkdir -p -- "${TEST_BASE}"
TEST_ROOT="$(mktemp -d "${TEST_BASE}/run-XXXXXX")"
TEST_BIN_DIR="${TEST_ROOT}/bin"
TEST_COUNT=0
mkdir -p -- "${TEST_BIN_DIR}"

cat >"${TEST_BIN_DIR}/readlink" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "-f" && "${2:-}" =~ ^/proc/[0-9]+/exe$ && -n "${FAKE_APP_DIR:-}" ]]; then
  printf '%s\n' "${FAKE_APP_DIR}/build/new-api"
  exit 0
fi
exec /usr/bin/readlink "$@"
EOF
chmod 755 -- "${TEST_BIN_DIR}/readlink"

cat >"${TEST_BIN_DIR}/uname" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  -s)
    printf '%s\n' "${FAKE_UNAME_S:-Linux}"
    ;;
  -m)
    printf '%s\n' "${FAKE_UNAME_M:-x86_64}"
    ;;
  *)
    printf '%s\n' "${FAKE_UNAME_S:-Linux}"
    ;;
esac
EOF
chmod 755 -- "${TEST_BIN_DIR}/uname"
export PATH="${TEST_BIN_DIR}:${PATH}"

cleanup() {
  local pid=""
  if [[ "${KEEP_PATCHCTL_TEST_ROOT:-0}" == "1" ]]; then
    printf 'Test artifacts retained at %s\n' "${TEST_ROOT}" >&2
    return
  fi
  while IFS= read -r -d '' pid_file; do
    pid="$(tr -d '[:space:]' <"${pid_file}" 2>/dev/null || true)"
    [[ "${pid}" =~ ^[0-9]+$ ]] && kill "${pid}" 2>/dev/null || true
  done < <(find "${TEST_ROOT}" -name new-api.pid -type f -print0 2>/dev/null)
  rm -rf -- "${TEST_ROOT}"
}

trap cleanup EXIT

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  return 1
}

assert_file_contains() {
  local file="$1"
  local expected="$2"
  grep -Fqx -- "${expected}" "${file}" ||
    fail "${file} does not contain exact line: ${expected}"
}

assert_file_has_text() {
  local file="$1"
  local expected="$2"
  grep -Fq -- "${expected}" "${file}" ||
    fail "${file} does not contain: ${expected}"
}

assert_line_count() {
  local file="$1"
  local expected_line="$2"
  local expected_count="$3"
  local actual_count=0
  actual_count="$(grep -Fxc -- "${expected_line}" "${file}" || true)"
  [[ "${actual_count}" == "${expected_count}" ]] ||
    fail "${file}: expected ${expected_count} occurrence(s) of '${expected_line}', got ${actual_count}"
}

assert_file_equals() {
  local file="$1"
  local expected="$2"
  local actual=""
  actual="$(cat -- "${file}")"
  [[ "${actual}" == "${expected}" ]] ||
    fail "${file}: expected '${expected}', got '${actual}'"
}

assert_files_equal() {
  local left="$1"
  local right="$2"
  cmp -s -- "${left}" "${right}" ||
    fail "${left} and ${right} differ"
}

write_service_script() {
  local output="$1"
  local flavor="$2"
  cat >"${output}" <<EOF
#!/usr/bin/env bash
set -Eeuo pipefail
FLAVOR="${flavor}"
case "\${1:-}" in
  status)
    pid="\$(cat -- "\${FAKE_APP_DIR}/.run/new-api.pid" 2>/dev/null || true)"
    [[ "\$(cat -- "\${FAKE_SERVICE_STATE}")" == "running" ]]
    [[ "\${pid}" =~ ^[0-9]+$ ]] && kill -0 "\${pid}" 2>/dev/null
    [[ ! ("\${FLAVOR}" == "new" && "\${FAKE_SERVICE_BEHAVIOR:-}" == "fail-status") ]]
    ;;
  stop)
    pid="\$(cat -- "\${FAKE_APP_DIR}/.run/new-api.pid" 2>/dev/null || true)"
    if [[ "\${pid}" =~ ^[0-9]+$ ]]; then
      kill "\${pid}" 2>/dev/null || true
      wait "\${pid}" 2>/dev/null || true
    fi
    rm -f -- \
      "\${FAKE_APP_DIR}/.run/new-api.pid" \
      "\${FAKE_APP_DIR}/.run/new-api.port"
    printf 'stopped\n' >"\${FAKE_SERVICE_STATE}"
    printf '%s:stop\n' "\${FLAVOR}" >>"\${FAKE_SERVICE_EVENTS}"
    ;;
  start)
    mkdir -p -- "\${FAKE_APP_DIR}/.run"
    "\${FAKE_APP_DIR}/build/new-api" 300 >/dev/null 2>&1 &
    pid="\$!"
    printf '%s\n' "\${pid}" >"\${FAKE_APP_DIR}/.run/new-api.pid"
    printf '3000\n' >"\${FAKE_APP_DIR}/.run/new-api.port"
    printf 'running\n' >"\${FAKE_SERVICE_STATE}"
    printf '%s:start\n' "\${FLAVOR}" >>"\${FAKE_SERVICE_EVENTS}"
    if [[ "\${FLAVOR}" == "new" && "\${FAKE_SERVICE_BEHAVIOR:-}" == "change-config" ]]; then
      printf 'changed=true\n' >>"\${FAKE_SERVICE_CONFIG}"
    fi
    ;;
  *)
    exit 64
    ;;
esac
EOF
  chmod 755 -- "${output}"
}

write_test_binary() {
  local output="$1"
  local flavor="$2"
  cat >"${output}" <<EOF
#!/usr/bin/env bash
printf '%s\n' "${flavor}" >/dev/null
exec sleep "\${1:-300}"
EOF
  chmod 755 -- "${output}"
}

write_patchdb() {
  local output="$1"
  cat >"${output}" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
command="${1:-}"
shift || true
case "${command}" in
  identity)
    if [[ "${FAKE_APP_IDENTITY_FAIL:-}" == "1" && -z "${PATCH_DB_TYPE:-}" ]]; then
      exit 73
    fi
    printf 'DB_TYPE=sqlite\n'
    printf 'DB_SUMMARY=sqlite|test\n'
    printf 'DB_FINGERPRINT=test-fingerprint\n'
    ;;
  inspect)
    printf 'SCHEMA_STATE=%s\n' "$(cat -- "${FAKE_SCHEMA_STATE}")"
    ;;
  switches)
    ;;
  backup)
    if [[ "${FAKE_PATCHDB_BEHAVIOR:-}" == "backup-fail" ]]; then
      exit 71
    fi
    output=""
    while (( $# > 0 )); do
      case "$1" in
        --output)
          output="$2"
          shift 2
          ;;
        *)
          shift
          ;;
      esac
    done
    cp -- "${PATCH_SQLITE_PATH}" "${output}"
    ;;
  migrate)
    if [[ "${FAKE_PATCHDB_BEHAVIOR:-}" == "partial-migration" ]]; then
      printf 'compatible-partial\n' >"${FAKE_SCHEMA_STATE}"
      exit 72
    fi
    printf 'target\n' >"${FAKE_SCHEMA_STATE}"
    ;;
  verify)
    [[ "$(cat -- "${FAKE_SCHEMA_STATE}")" == "target" ]]
    ;;
  *)
    exit 64
    ;;
esac
EOF
  chmod 755 -- "${output}"
}

prepare_case() {
  local name="$1"
  local initial_service_state="$2"
  CASE_ROOT="${TEST_ROOT}/${name}"
  PACKAGE_DIR="${CASE_ROOT}/package"
  APP_DIR="${CASE_ROOT}/app"
  BACKUP_ROOT="${CASE_ROOT}/backups"
  mkdir -p \
    "${PACKAGE_DIR}/assets" \
    "${PACKAGE_DIR}/bin/linux-amd64" \
    "${PACKAGE_DIR}/DDL" \
    "${APP_DIR}/build" \
    "${BACKUP_ROOT}"

  cp -- "${PATCHCTL_SOURCE}" "${PACKAGE_DIR}/patchctl.sh"
  chmod 755 -- "${PACKAGE_DIR}/patchctl.sh"
  cat >"${PACKAGE_DIR}/manifest.env" <<'EOF'
PATCH_ID=new-api-20260716
PATCH_DATE=2026-07-16
BASELINE_COMMIT=7c28993f6bd9e92616f3f578212577f8b7c40b45
SUPPORTED_OS=linux
SUPPORTED_ARCHES=amd64,arm64
SUPPORTED_DATABASES=sqlite,mysql,postgres
EOF
  printf 'test ddl\n' >"${PACKAGE_DIR}/DDL/test.sql"
  write_test_binary "${PACKAGE_DIR}/bin/linux-amd64/new-api" "new"
  write_patchdb "${PACKAGE_DIR}/bin/linux-amd64/patchdb"
  write_service_script "${PACKAGE_DIR}/assets/new-api.sh" "new"
  printf 'source patch\n' >"${PACKAGE_DIR}/source.patch"
  printf 'included\n' >"${PACKAGE_DIR}/FILES_INCLUDED.txt"
  printf 'excluded\n' >"${PACKAGE_DIR}/FILES_EXCLUDED.txt"

  write_test_binary "${CASE_ROOT}/old-new-api" "old"
  cp -- "${CASE_ROOT}/old-new-api" "${APP_DIR}/build/new-api"
  write_service_script "${APP_DIR}/new-api.sh" "old"
  printf 'APP_ENV=production\n' >"${APP_DIR}/.env"
  printf 'database\n' >"${APP_DIR}/one-api.db"
  printf 'stopped\n' >"${CASE_ROOT}/service-state"
  : >"${CASE_ROOT}/service-events"
  printf 'legacy\n' >"${CASE_ROOT}/schema-state"

  (
    cd -- "${PACKAGE_DIR}"
    sha256sum \
      patchctl.sh \
      manifest.env \
      DDL/test.sql \
      assets/new-api.sh \
      bin/linux-amd64/new-api \
      bin/linux-amd64/patchdb \
      source.patch \
      FILES_INCLUDED.txt \
      FILES_EXCLUDED.txt >SHA256SUMS
  )

  export PATCH_APP_DIR="${APP_DIR}"
  export PATCH_BACKUP_ROOT="${BACKUP_ROOT}"
  export PATCH_DB_TYPE="sqlite"
  export PATCH_SQLITE_PATH="${APP_DIR}/one-api.db"
  export FAKE_APP_DIR="${APP_DIR}"
  export FAKE_SERVICE_STATE="${CASE_ROOT}/service-state"
  export FAKE_SERVICE_EVENTS="${CASE_ROOT}/service-events"
  export FAKE_SERVICE_CONFIG="${APP_DIR}/.env"
  export FAKE_SCHEMA_STATE="${CASE_ROOT}/schema-state"
  export PATCH_EXPECTED_DB_FINGERPRINT="test-fingerprint"
  export PATCH_CONFIRM_DEPLOY="new-api-20260716:test-fingerprint"
  unset FAKE_APP_IDENTITY_FAIL FAKE_PATCHDB_BEHAVIOR FAKE_SERVICE_BEHAVIOR
  unset FAKE_UNAME_S FAKE_UNAME_M
  if [[ "${initial_service_state}" == "running" ]]; then
    "${APP_DIR}/new-api.sh" start
  fi
}

run_deploy() {
  "${PACKAGE_DIR}/patchctl.sh" deploy --non-interactive \
    --confirm-writers-stopped \
    >"${CASE_ROOT}/stdout.log" 2>"${CASE_ROOT}/stderr.log"
}

test_backup_failure_restarts_old_service() {
  prepare_case "backup-failure" "running"
  export FAKE_PATCHDB_BEHAVIOR="backup-fail"
  if run_deploy; then
    fail "deployment unexpectedly succeeded"
  fi
  assert_file_equals "${CASE_ROOT}/service-state" "running"
  assert_files_equal "${APP_DIR}/build/new-api" "${CASE_ROOT}/old-new-api"
  assert_file_contains "${CASE_ROOT}/service-events" "old:stop"
  assert_line_count "${CASE_ROOT}/service-events" "old:start" 2
}

test_partial_migration_keeps_service_stopped() {
  prepare_case "partial-migration" "running"
  export FAKE_PATCHDB_BEHAVIOR="partial-migration"
  if run_deploy; then
    fail "deployment unexpectedly succeeded"
  fi
  assert_file_equals "${CASE_ROOT}/service-state" "stopped"
  assert_files_equal "${APP_DIR}/build/new-api" "${CASE_ROOT}/old-new-api"
  assert_file_equals "${CASE_ROOT}/schema-state" "compatible-partial"
  assert_line_count "${CASE_ROOT}/service-events" "old:start" 1
}

test_post_start_validation_failure_restores_old_files_and_stops_service() {
  prepare_case "post-start-validation-failure" "running"
  export FAKE_SERVICE_BEHAVIOR="fail-status"
  if run_deploy; then
    fail "deployment unexpectedly succeeded"
  fi
  assert_file_equals "${CASE_ROOT}/service-state" "stopped"
  assert_files_equal "${APP_DIR}/build/new-api" "${CASE_ROOT}/old-new-api"
  assert_file_contains "${CASE_ROOT}/service-events" "new:start"
  assert_file_contains "${CASE_ROOT}/service-events" "new:stop"
  assert_line_count "${CASE_ROOT}/service-events" "old:start" 1
}

test_stopped_service_stays_stopped_across_deploy_and_rollback() {
  prepare_case "stopped-deploy-rollback" "stopped"
  run_deploy
  assert_file_equals "${CASE_ROOT}/service-state" "stopped"
  assert_files_equal \
    "${APP_DIR}/build/new-api" \
    "${PACKAGE_DIR}/bin/linux-amd64/new-api"

  backup_dir="$(tr -d '\r\n' <"${BACKUP_ROOT}/LATEST_DEPLOY")"
  export PATCH_CONFIRM_ROLLBACK="new-api-20260716:$(basename -- "${backup_dir}")"
  "${PACKAGE_DIR}/patchctl.sh" rollback --non-interactive --backup-dir "${backup_dir}" \
    >"${CASE_ROOT}/rollback-stdout.log" 2>"${CASE_ROOT}/rollback-stderr.log"
  assert_file_equals "${CASE_ROOT}/service-state" "stopped"
  assert_files_equal "${APP_DIR}/build/new-api" "${CASE_ROOT}/old-new-api"
}

test_config_change_restores_old_files_and_keeps_service_stopped() {
  prepare_case "config-change" "running"
  export FAKE_SERVICE_BEHAVIOR="change-config"
  if run_deploy; then
    fail "deployment unexpectedly succeeded"
  fi
  assert_file_equals "${CASE_ROOT}/service-state" "stopped"
  assert_files_equal "${APP_DIR}/build/new-api" "${CASE_ROOT}/old-new-api"
  assert_file_contains "${CASE_ROOT}/service-events" "new:start"
  assert_file_contains "${CASE_ROOT}/service-events" "new:stop"
  assert_line_count "${CASE_ROOT}/service-events" "old:start" 1
}

test_unsupported_host_fails_before_configuration() {
  prepare_case "unsupported-host" "stopped"
  export FAKE_UNAME_S="Darwin"
  if "${PACKAGE_DIR}/patchctl.sh" precheck --non-interactive \
    >"${CASE_ROOT}/stdout.log" 2>"${CASE_ROOT}/stderr.log"; then
    fail "precheck unexpectedly succeeded on an unsupported host"
  fi
  assert_file_has_text "${CASE_ROOT}/stderr.log" "不支持的操作系统"
  assert_file_has_text "${CASE_ROOT}/stderr.log" "Darwin"
}

test_noninteractive_requires_expected_database_fingerprint() {
  prepare_case "missing-expected-database-fingerprint" "stopped"
  export FAKE_APP_IDENTITY_FAIL=1
  unset PATCH_EXPECTED_DB_FINGERPRINT
  if "${PACKAGE_DIR}/patchctl.sh" precheck \
    --non-interactive \
    --confirm-writers-stopped \
    >"${CASE_ROOT}/stdout.log" 2>"${CASE_ROOT}/stderr.log"; then
    fail "precheck unexpectedly succeeded without a database identity binding"
  fi
  assert_file_has_text \
    "${CASE_ROOT}/stderr.log" \
    "非交互模式必须提供 PATCH_EXPECTED_DB_FINGERPRINT"
}

test_missing_linux_service_script_is_installed_and_removed_on_rollback() {
  prepare_case "missing-linux-service-script" "stopped"
  rm -f -- "${APP_DIR}/new-api.sh"

  run_deploy
  [[ -f "${APP_DIR}/new-api.sh" ]] ||
    fail "deploy did not install the Linux service script"

  backup_dir="$(tr -d '\r\n' <"${BACKUP_ROOT}/LATEST_DEPLOY")"
  assert_file_contains "${backup_dir}/backup-manifest.env" "HAS_SCRIPT=0"

  export PATCH_CONFIRM_ROLLBACK="new-api-20260716:$(basename -- "${backup_dir}")"
  "${PACKAGE_DIR}/patchctl.sh" rollback \
    --non-interactive \
    --backup-dir "${backup_dir}" \
    >"${CASE_ROOT}/rollback-stdout.log" 2>"${CASE_ROOT}/rollback-stderr.log"

  [[ ! -e "${APP_DIR}/new-api.sh" ]] ||
    fail "rollback did not remove the service script installed by the Patch"
  assert_files_equal "${APP_DIR}/build/new-api" "${CASE_ROOT}/old-new-api"
}

run_test() {
  local test_name="$1"
  "${test_name}"
  TEST_COUNT=$((TEST_COUNT + 1))
  printf 'PASS: %s\n' "${test_name}"
}

run_test test_backup_failure_restarts_old_service
run_test test_partial_migration_keeps_service_stopped
run_test test_post_start_validation_failure_restores_old_files_and_stops_service
run_test test_stopped_service_stays_stopped_across_deploy_and_rollback
run_test test_config_change_restores_old_files_and_keeps_service_stopped
run_test test_unsupported_host_fails_before_configuration
run_test test_noninteractive_requires_expected_database_fingerprint
run_test test_missing_linux_service_script_is_installed_and_removed_on_rollback

printf 'PASS: %s patchctl integration tests\n' "${TEST_COUNT}"
