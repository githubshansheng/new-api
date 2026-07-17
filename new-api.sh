#!/usr/bin/env bash

set -Eeuo pipefail

if [[ -n "${NEW_API_ROOT_DIR:-}" ]]; then
  [[ -d "${NEW_API_ROOT_DIR}" ]] || {
    printf 'ERROR: NEW_API_ROOT_DIR is not a directory: %s\n' "${NEW_API_ROOT_DIR}" >&2
    exit 1
  }
  ROOT_DIR="$(cd -- "${NEW_API_ROOT_DIR}" && pwd -P)"
else
  ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
fi
BUILD_DIR="${ROOT_DIR}/build"
RUN_DIR="${ROOT_DIR}/.run"
LOG_DIR="${ROOT_DIR}/logs"
BINARY_PATH="${BUILD_DIR}/new-api"
PID_FILE="${RUN_DIR}/new-api.pid"
PORT_FILE="${RUN_DIR}/new-api.port"
STARTTIME_FILE="${RUN_DIR}/new-api.starttime"
BOOT_ID_FILE="${RUN_DIR}/new-api.boot-id"
STDOUT_LOG="${RUN_DIR}/new-api.stdout.log"
STDERR_LOG="${RUN_DIR}/new-api.stderr.log"
ACTION=""
REQUESTED_PORT=""

log_step() {
  printf '==> %s\n' "$1"
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'ERROR: Required command not found: %s\n' "$1" >&2
    exit 1
  fi
}

require_positive_timeout() {
  local value="$1"
  local name="$2"
  if [[ ! "${value}" =~ ^[0-9]+$ ]] || (( 10#${value} < 1 || 10#${value} > 3600 )); then
    printf 'ERROR: %s must be an integer between 1 and 3600.\n' "${name}" >&2
    return 1
  fi
}

ensure_runtime_paths() {
  for directory in "${RUN_DIR}" "${LOG_DIR}"; do
    if [[ -L "${directory}" ]]; then
      printf 'ERROR: Managed directory must not be a symbolic link: %s\n' "${directory}" >&2
      return 1
    fi
    mkdir -p -- "${directory}"
    if [[ ! -d "${directory}" ]]; then
      printf 'ERROR: Managed path is not a directory: %s\n' "${directory}" >&2
      return 1
    fi
  done
  for file in \
    "${PID_FILE}" \
    "${PORT_FILE}" \
    "${STARTTIME_FILE}" \
    "${BOOT_ID_FILE}" \
    "${STDOUT_LOG}" \
    "${STDERR_LOG}"; do
    if [[ -L "${file}" ]]; then
      printf 'ERROR: Managed file must not be a symbolic link: %s\n' "${file}" >&2
      return 1
    fi
  done
}

normalize_port() {
  local value="$1"
  local source="$2"

  if [[ ! "${value}" =~ ^[0-9]{1,5}$ ]]; then
    printf 'ERROR: %s must be an integer between 1 and 65535.\n' "${source}" >&2
    return 1
  fi

  local normalized_port=$((10#${value}))
  if (( normalized_port < 1 || normalized_port > 65535 )); then
    printf 'ERROR: %s must be between 1 and 65535.\n' "${source}" >&2
    return 1
  fi

  printf '%s' "${normalized_port}"
}

parse_args() {
  while (( $# > 0 )); do
    case "$1" in
      --port)
        if [[ -n "${REQUESTED_PORT}" ]]; then
          printf 'ERROR: --port may only be specified once.\n' >&2
          exit 1
        fi
        if (( $# < 2 )); then
          printf 'ERROR: --port requires a value.\n' >&2
          exit 1
        fi
        REQUESTED_PORT="$(normalize_port "$2" "--port")"
        shift 2
        ;;
      --port=*)
        if [[ -n "${REQUESTED_PORT}" ]]; then
          printf 'ERROR: --port may only be specified once.\n' >&2
          exit 1
        fi
        REQUESTED_PORT="$(normalize_port "${1#*=}" "--port")"
        shift
        ;;
      build|start|stop|restart|rebuild|status|logs|help|-h|--help)
        if [[ -n "${ACTION}" ]]; then
          printf 'ERROR: Multiple commands were specified: %s and %s.\n' "${ACTION}" "$1" >&2
          exit 1
        fi
        ACTION="$1"
        shift
        ;;
      *)
        printf 'ERROR: Unknown argument: %s\n' "$1" >&2
        exit 1
        ;;
    esac
  done

  ACTION="${ACTION:-start}"
}

app_version() {
  local version=""
  if [[ -f "${ROOT_DIR}/VERSION" ]]; then
    version="$(tr -d '\r\n' <"${ROOT_DIR}/VERSION")"
  fi
  if [[ -z "${version}" ]] && command -v git >/dev/null 2>&1; then
    version="$(git -C "${ROOT_DIR}" describe --tags --always --dirty 2>/dev/null || true)"
  fi
  printf '%s' "${version:-dev}"
}

initialize_build_environment() {
  local cache_dir="${ROOT_DIR}/.cache"
  mkdir -p \
    "${cache_dir}/tmp" \
    "${cache_dir}/go-build" \
    "${cache_dir}/go-mod" \
    "${cache_dir}/bun"

  export TMPDIR="${cache_dir}/tmp"
  export GOCACHE="${cache_dir}/go-build"
  export GOMODCACHE="${cache_dir}/go-mod"
  export BUN_INSTALL_CACHE_DIR="${cache_dir}/bun"
}

managed_pid() {
  if [[ ! -f "${PID_FILE}" ]]; then
    return 1
  fi

  if [[ -L "${PID_FILE}" || ! -f "${PID_FILE}" ]]; then
    printf 'ERROR: Invalid PID file: %s\n' "${PID_FILE}" >&2
    exit 1
  fi
  local managed_pid_value
  managed_pid_value="$(tr -d '[:space:]' <"${PID_FILE}")"
  if [[ ! "${managed_pid_value}" =~ ^[0-9]+$ ]]; then
    printf 'ERROR: Invalid managed PID value: %s\n' "${managed_pid_value}" >&2
    exit 1
  fi
  if ! kill -0 "${managed_pid_value}" 2>/dev/null; then
    if [[ -d "/proc/${managed_pid_value}" ]]; then
      printf 'ERROR: PID %s exists but cannot be inspected or signaled.\n' "${managed_pid_value}" >&2
      exit 1
    fi
    rm -f "${PID_FILE}" "${PORT_FILE}" "${STARTTIME_FILE}" "${BOOT_ID_FILE}"
    return 1
  fi

  if [[ ! -e "/proc/${managed_pid_value}/exe" ]]; then
    if ! kill -0 "${managed_pid_value}" 2>/dev/null; then
      rm -f "${PID_FILE}" "${PORT_FILE}" "${STARTTIME_FILE}" "${BOOT_ID_FILE}"
      return 1
    fi
    printf 'ERROR: Cannot verify executable for PID %s.\n' "${managed_pid_value}" >&2
    exit 1
  fi
  local actual_path expected_path
  if ! actual_path="$(readlink -f "/proc/${managed_pid_value}/exe" 2>/dev/null)"; then
    if ! kill -0 "${managed_pid_value}" 2>/dev/null; then
      rm -f "${PID_FILE}" "${PORT_FILE}" "${STARTTIME_FILE}" "${BOOT_ID_FILE}"
      return 1
    fi
    printf 'ERROR: Cannot resolve executable for PID %s.\n' "${managed_pid_value}" >&2
    exit 1
  fi
  expected_path="$(readlink -f "${BINARY_PATH}")"
  if [[ "${actual_path}" != "${expected_path}" ]]; then
    printf 'ERROR: PID %s belongs to another process: %s\n' "${managed_pid_value}" "${actual_path}" >&2
    exit 1
  fi
  if [[ ! -f "${STARTTIME_FILE}" || ! -f "${BOOT_ID_FILE}" ]]; then
    printf 'ERROR: Managed PID identity metadata is missing.\n' >&2
    exit 1
  fi
  local expected_starttime actual_starttime expected_boot_id actual_boot_id
  expected_starttime="$(tr -d '[:space:]' <"${STARTTIME_FILE}")"
  if ! actual_starttime="$(awk '{print $22}' "/proc/${managed_pid_value}/stat" 2>/dev/null)" ||
    [[ -z "${actual_starttime}" ]]; then
    if ! kill -0 "${managed_pid_value}" 2>/dev/null; then
      rm -f "${PID_FILE}" "${PORT_FILE}" "${STARTTIME_FILE}" "${BOOT_ID_FILE}"
      return 1
    fi
    printf 'ERROR: Cannot read start time for PID %s.\n' "${managed_pid_value}" >&2
    exit 1
  fi
  expected_boot_id="$(tr -d '[:space:]' <"${BOOT_ID_FILE}")"
  if ! actual_boot_id="$(tr -d '[:space:]' </proc/sys/kernel/random/boot_id 2>/dev/null)" ||
    [[ -z "${actual_boot_id}" ]]; then
    printf 'ERROR: Cannot read the Linux boot ID.\n' >&2
    exit 1
  fi
  if [[ -z "${expected_starttime}" ||
    "${expected_starttime}" != "${actual_starttime}" ||
    -z "${expected_boot_id}" ||
    "${expected_boot_id}" != "${actual_boot_id}" ]]; then
    printf 'ERROR: PID %s identity no longer matches the managed service.\n' "${managed_pid_value}" >&2
    exit 1
  fi

  printf '%s' "${managed_pid_value}"
}

configured_port() {
  if [[ -n "${REQUESTED_PORT}" ]]; then
    printf '%s' "${REQUESTED_PORT}"
    return
  fi

  if [[ "${PORT:-}" =~ ^[0-9]+$ ]]; then
    normalize_port "${PORT}" "PORT"
    return
  fi

  if [[ -f "${ROOT_DIR}/.env" ]]; then
    local configured_port
    configured_port="$(
      sed -nE "s/^[[:space:]]*PORT[[:space:]]*=[[:space:]]*['\"]?([0-9]+)['\"]?[[:space:]]*(#.*)?$/\1/p" \
        "${ROOT_DIR}/.env" | tail -n 1
    )"
    if [[ -n "${configured_port}" ]]; then
      normalize_port "${configured_port}" "PORT in .env"
      return
    fi
  fi

  printf '3000'
}

running_port() {
  if [[ -f "${PORT_FILE}" ]]; then
    local stored_port normalized_stored_port
    stored_port="$(tr -d '[:space:]' <"${PORT_FILE}")"
    if normalized_stored_port="$(normalize_port "${stored_port}" "stored runtime port" 2>/dev/null)"; then
      printf '%s' "${normalized_stored_port}"
      return
    fi
    rm -f "${PORT_FILE}"
  fi

  configured_port
}

build_app() {
  if managed_pid >/dev/null 2>&1; then
    printf "ERROR: The service is running. Use 'rebuild' to stop, build, and start it.\n" >&2
    exit 1
  fi

  require_command bun
  require_command go
  initialize_build_environment

  local version
  version="$(app_version)"

  log_step "Installing frontend dependencies"
  (
    cd "${ROOT_DIR}/web"
    bun install --frozen-lockfile
  )

  log_step "Building default frontend"
  (
    cd "${ROOT_DIR}/web/default"
    DISABLE_ESLINT_PLUGIN=true VITE_REACT_APP_VERSION="${version}" bun run build
  )

  log_step "Building classic frontend"
  (
    cd "${ROOT_DIR}/web/classic"
    VITE_REACT_APP_VERSION="${version}" bun run build
  )

  log_step "Building backend with embedded frontend assets"
  mkdir -p "${BUILD_DIR}"
  (
    cd "${ROOT_DIR}"
    go build \
      -trimpath \
      -ldflags "-s -w -X github.com/QuantumNous/new-api/common.Version=${version}" \
      -o "${BINARY_PATH}" \
      .
  )

  printf 'Build complete: %s\n' "${BINARY_PATH}"
}

wait_for_startup() {
  local managed_pid_value="$1"
  local port="$2"
  local timeout="${STARTUP_TIMEOUT_SECONDS:-60}"
  local url="http://127.0.0.1:${port}/api/status"
  require_positive_timeout "${timeout}" "STARTUP_TIMEOUT_SECONDS"
  if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
    printf 'ERROR: curl or wget is required for startup health checks.\n' >&2
    return 1
  fi
  local deadline=$((SECONDS + timeout))

  while (( SECONDS < deadline )); do
    if ! kill -0 "${managed_pid_value}" 2>/dev/null; then
      printf 'ERROR: Process exited during startup. Check %s\n' "${STDERR_LOG}" >&2
      return 1
    fi

    if command -v curl >/dev/null 2>&1; then
      if curl --silent --show-error --fail --max-time 2 "${url}" >/dev/null 2>&1; then
        printf 'Started: %s\n' "${url}"
        return
      fi
    elif command -v wget >/dev/null 2>&1; then
      if wget -q -T 2 -O /dev/null "${url}"; then
        printf 'Started: %s\n' "${url}"
        return
      fi
    fi

    sleep 0.5
  done

  printf 'ERROR: Startup timed out after %s seconds; check logs.\n' "${timeout}" >&2
  return 1
}

terminate_managed_process() {
  local running_pid="$1"
  local signal="$2"
  kill "-${signal}" -- "-${running_pid}" 2>/dev/null ||
    kill "-${signal}" "${running_pid}" 2>/dev/null
}

start_app() {
  local running_pid
  if running_pid="$(managed_pid)"; then
    local active_port
    active_port="$(running_port)"
    if [[ -n "${REQUESTED_PORT}" && "${REQUESTED_PORT}" != "${active_port}" ]]; then
      printf "ERROR: Service is already running on port %s. Use 'restart --port %s' to change it.\n" \
        "${active_port}" "${REQUESTED_PORT}" >&2
      exit 1
    fi
    printf 'Already running (PID %s, http://127.0.0.1:%s).\n' "${running_pid}" "${active_port}"
    return
  fi

  if [[ ! -x "${BINARY_PATH}" ]]; then
    build_app
  fi

  require_command setsid
  ensure_runtime_paths
  touch -- "${STDOUT_LOG}" "${STDERR_LOG}"

  local port
  port="$(configured_port)"

  log_step "Starting service"
  (
    cd "${ROOT_DIR}"
    PORT="${port}" nohup setsid "${BINARY_PATH}" \
      --port "${port}" \
      --log-dir "${LOG_DIR}" \
      >>"${STDOUT_LOG}" 2>>"${STDERR_LOG}" &
    printf '%s' "$!" >"${PID_FILE}"
    printf '%s' "${port}" >"${PORT_FILE}"
  )

  running_pid="$(cat "${PID_FILE}")"
  local process_starttime=""
  if ! process_starttime="$(awk '{print $22}' "/proc/${running_pid}/stat" 2>/dev/null)" ||
    [[ -z "${process_starttime}" ]] ||
    ! kill -0 "${running_pid}" 2>/dev/null; then
    rm -f "${PID_FILE}" "${PORT_FILE}" "${STARTTIME_FILE}" "${BOOT_ID_FILE}"
    printf 'ERROR: Process exited before startup identity could be recorded. Check %s\n' \
      "${STDERR_LOG}" >&2
    exit 1
  fi
  local current_boot_id=""
  if ! current_boot_id="$(tr -d '[:space:]' </proc/sys/kernel/random/boot_id 2>/dev/null)" ||
    [[ -z "${current_boot_id}" ]]; then
    terminate_managed_process "${running_pid}" TERM || true
    rm -f "${PID_FILE}" "${PORT_FILE}" "${STARTTIME_FILE}" "${BOOT_ID_FILE}"
    printf 'ERROR: Cannot read the Linux boot ID.\n' >&2
    exit 1
  fi
  printf '%s' "${process_starttime}" >"${STARTTIME_FILE}"
  printf '%s' "${current_boot_id}" >"${BOOT_ID_FILE}"
  if ! wait_for_startup "${running_pid}" "${port}"; then
    tail -n 30 "${STDERR_LOG}" >&2 || true
    terminate_managed_process "${running_pid}" TERM || true
    sleep 1
    if kill -0 "${running_pid}" 2>/dev/null; then
      terminate_managed_process "${running_pid}" KILL || true
    fi
    if kill -0 "${running_pid}" 2>/dev/null; then
      printf 'ERROR: Failed startup process is still running; PID metadata was retained.\n' >&2
    else
      rm -f "${PID_FILE}" "${PORT_FILE}" "${STARTTIME_FILE}" "${BOOT_ID_FILE}"
    fi
    exit 1
  fi
}

stop_app() {
  local running_pid
  if ! running_pid="$(managed_pid)"; then
    printf 'Service is not running.\n'
    return
  fi

  log_step "Stopping service (PID ${running_pid})"
  terminate_managed_process "${running_pid}" TERM

  local timeout="${STOP_TIMEOUT_SECONDS:-130}"
  require_positive_timeout "${timeout}" "STOP_TIMEOUT_SECONDS"
  local deadline=$((SECONDS + timeout))
  while kill -0 "${running_pid}" 2>/dev/null; do
    if (( SECONDS >= deadline )); then
      printf 'Graceful shutdown timed out; forcing process termination.\n' >&2
      terminate_managed_process "${running_pid}" KILL || true
      break
    fi
    sleep 0.5
  done

  local kill_deadline=$((SECONDS + 10))
  while kill -0 "${running_pid}" 2>/dev/null && (( SECONDS < kill_deadline )); do
    sleep 0.2
  done
  if kill -0 "${running_pid}" 2>/dev/null; then
    printf 'ERROR: Process %s is still running after SIGKILL; PID metadata was retained.\n' \
      "${running_pid}" >&2
    return 1
  fi
  rm -f "${PID_FILE}" "${PORT_FILE}" "${STARTTIME_FILE}" "${BOOT_ID_FILE}"
  printf 'Stopped.\n'
}

show_status() {
  local running_pid
  if ! running_pid="$(managed_pid)"; then
    printf 'Status: stopped\n'
    return 1
  fi

  local url="http://127.0.0.1:$(running_port)/api/status"
  if command -v curl >/dev/null 2>&1 && curl --silent --fail --max-time 3 "${url}" >/dev/null 2>&1; then
    printf 'Status: running (PID %s, HTTP healthy, %s)\n' "${running_pid}" "${url}"
  elif command -v wget >/dev/null 2>&1 && wget -q -T 3 -O /dev/null "${url}"; then
    printf 'Status: running (PID %s, HTTP healthy, %s)\n' "${running_pid}" "${url}"
  else
    printf 'Status: process running (PID %s), but health check failed: %s\n' "${running_pid}" "${url}"
    return 1
  fi
}

show_logs() {
  mkdir -p "${RUN_DIR}"
  touch "${STDOUT_LOG}" "${STDERR_LOG}"
  printf 'Following stdout and stderr logs. Press Ctrl+C to stop.\n'
  tail -n 100 -F "${STDOUT_LOG}" "${STDERR_LOG}"
}

show_help() {
  cat <<'EOF'
Usage:
  ./new-api.sh [command] [--port <port>]

Examples:
  ./new-api.sh build
  ./new-api.sh start --port 3000
  ./new-api.sh stop
  ./new-api.sh restart --port 8080
  ./new-api.sh rebuild --port 8080
  ./new-api.sh status
  ./new-api.sh logs

Commands:
  build     Build both frontends and the Go binary.
  start     Start the existing binary; build first when it is missing.
  stop      Gracefully stop the managed background process.
  restart   Restart without rebuilding.
  rebuild   Stop, rebuild everything, and start.
  status    Show process and HTTP health status.
  logs      Follow stdout and stderr logs.

Options:
  --port    Set the startup and health-check port (1-65535).
            This overrides PORT and the PORT value in .env.

Runtime configuration is loaded from .env by the application.
EOF
}

parse_args "$@"
cd "${ROOT_DIR}"
case "${ACTION}" in
  build)
    build_app
    ;;
  start)
    start_app
    ;;
  stop)
    stop_app
    ;;
  restart)
    if [[ -z "${REQUESTED_PORT}" ]] && managed_pid >/dev/null 2>&1; then
      REQUESTED_PORT="$(running_port)"
    fi
    stop_app
    start_app
    ;;
  rebuild)
    if [[ -z "${REQUESTED_PORT}" ]] && managed_pid >/dev/null 2>&1; then
      REQUESTED_PORT="$(running_port)"
    fi
    stop_app
    build_app
    start_app
    ;;
  status)
    show_status
    ;;
  logs)
    show_logs
    ;;
  help|-h|--help)
    show_help
    ;;
  *)
    printf 'ERROR: Unknown command: %s\n\n' "${ACTION}" >&2
    show_help >&2
    exit 1
    ;;
esac
