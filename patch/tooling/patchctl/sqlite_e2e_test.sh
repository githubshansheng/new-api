#!/usr/bin/env bash

set -Eeuo pipefail

REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
PATCHCTL_SOURCE="${REPOSITORY_ROOT}/patch/2026-07-16/patchctl.sh"
DDL_SOURCE="${REPOSITORY_ROOT}/patch/DDL"
TEST_BASE="${REPOSITORY_ROOT}/.cache/patchctl-sqlite-e2e"
mkdir -p -- "${TEST_BASE}"
TEST_ROOT="$(mktemp -d "${TEST_BASE}/run-XXXXXX")"
PACKAGE_DIR="${TEST_ROOT}/package"
APP_DIR="${TEST_ROOT}/app"
BACKUP_ROOT="${TEST_ROOT}/backups"
KERNEL_NAME="$(uname -s)"
ARCH="amd64"

case "${KERNEL_NAME}" in
  Linux)
    PLATFORM="linux"
    BINARY_NAME="new-api"
    SERVICE_SCRIPT_NAME="new-api.sh"
    ;;
  MINGW*|MSYS*|CYGWIN*)
    PLATFORM="windows"
    BINARY_NAME="new-api.exe"
    SERVICE_SCRIPT_NAME="new-api.ps1"
    ;;
  *)
    printf 'SKIP: unsupported SQLite e2e host %s\n' "${KERNEL_NAME}"
    exit 0
    ;;
esac

PACKAGE_BIN_DIR="${PACKAGE_DIR}/bin/${PLATFORM}-${ARCH}"
PATCHDB_BIN="${PACKAGE_BIN_DIR}/patchdb"
if [[ "${PLATFORM}" == "windows" ]]; then
  PATCHDB_BIN="${PATCHDB_BIN}.exe"
fi
PATCH_BINARY="${PACKAGE_BIN_DIR}/${BINARY_NAME}"
PACKAGE_SERVICE_SCRIPT="${PACKAGE_DIR}/assets/${SERVICE_SCRIPT_NAME}"
APP_BINARY="${APP_DIR}/build/${BINARY_NAME}"
APP_SERVICE_SCRIPT="${APP_DIR}/${SERVICE_SCRIPT_NAME}"

cleanup() {
  if [[ "${KEEP_PATCHCTL_SQLITE_E2E_ROOT:-0}" == "1" ]]; then
    printf 'Test artifacts retained at %s\n' "${TEST_ROOT}" >&2
    return
  fi
  rm -rf -- "${TEST_ROOT}"
}

trap cleanup EXIT

mkdir -p \
  "${PACKAGE_DIR}/assets" \
  "${PACKAGE_BIN_DIR}" \
  "${PACKAGE_DIR}/DDL" \
  "${APP_DIR}/build" \
  "${BACKUP_ROOT}"

cp -- "${PATCHCTL_SOURCE}" "${PACKAGE_DIR}/patchctl.sh"
cp -- "${DDL_SOURCE}"/* "${PACKAGE_DIR}/DDL/"
chmod 755 -- "${PACKAGE_DIR}/patchctl.sh"
cat >"${PACKAGE_DIR}/manifest.env" <<EOF
PATCH_ID=new-api-20260716
PATCH_DATE=2026-07-16
BASELINE_COMMIT=7c28993f6bd9e92616f3f578212577f8b7c40b45
SUPPORTED_OS=${PLATFORM}
SUPPORTED_ARCHES=${ARCH}
SUPPORTED_DATABASES=sqlite,mysql,postgres
EOF
printf 'source patch\n' >"${PACKAGE_DIR}/source.patch"
printf 'included\n' >"${PACKAGE_DIR}/FILES_INCLUDED.txt"
printf 'excluded\n' >"${PACKAGE_DIR}/FILES_EXCLUDED.txt"

GOOS="${PLATFORM}" \
  GOARCH="${ARCH}" \
  CGO_ENABLED=0 \
  GOCACHE="${REPOSITORY_ROOT}/.cache/go-build" \
  go build \
  -o "${PATCHDB_BIN}" \
  "${REPOSITORY_ROOT}/patch/tooling/patchdb"

printf 'new application binary\n' >"${PATCH_BINARY}"
if [[ "${PLATFORM}" == "windows" ]]; then
  cp -- "${REPOSITORY_ROOT}/new-api.ps1" "${PACKAGE_SERVICE_SCRIPT}"
else
  cat >"${PACKAGE_SERVICE_SCRIPT}" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  status)
    exit 1
    ;;
  stop)
    exit 0
    ;;
  start)
    exit 1
    ;;
  *)
    exit 64
    ;;
esac
EOF
  chmod 755 -- "${PACKAGE_SERVICE_SCRIPT}"
fi
chmod 755 -- "${PATCHDB_BIN}" "${PATCH_BINARY}"

printf 'old application binary\n' >"${APP_BINARY}"
printf 'old service script\n' >"${APP_SERVICE_SCRIPT}"
chmod 755 -- "${APP_BINARY}" "${APP_SERVICE_SCRIPT}"
cp -- "${APP_BINARY}" "${TEST_ROOT}/${BINARY_NAME}.old"

DATABASE_PATH="${APP_DIR}/one-api.db"
NATIVE_DATABASE_PATH="${DATABASE_PATH}"
NATIVE_APP_DIR="${APP_DIR}"
NATIVE_DDL_DIR="${PACKAGE_DIR}/DDL"
if [[ "${PLATFORM}" == "windows" ]]; then
  NATIVE_DATABASE_PATH="$(cygpath -w "${DATABASE_PATH}")"
  NATIVE_APP_DIR="$(cygpath -w "${APP_DIR}")"
  NATIVE_DDL_DIR="$(cygpath -w "${PACKAGE_DIR}/DDL")"
fi
cat >"${APP_DIR}/.env" <<EOF
SQL_DSN=local
SQLITE_PATH=${NATIVE_DATABASE_PATH}
EOF

DATABASE_PATH="${NATIVE_DATABASE_PATH}" python - <<'PY'
import os
import sqlite3

database = sqlite3.connect(os.environ["DATABASE_PATH"])
database.executescript(
    """
    CREATE TABLE options ("key" text PRIMARY KEY, "value" text);
    INSERT INTO options ("key", "value") VALUES
      ('LiandongEnabled', 'false'),
      ('LiandongCreateEnabled', 'false'),
      ('LiandongReconcileEnabled', 'false'),
      ('LiandongFulfillEnabled', 'false'),
      ('LiandongIframeEnabled', 'false');

    CREATE TABLE liandong_products (
      id integer PRIMARY KEY AUTOINCREMENT,
      business_type varchar(32) NOT NULL,
      name varchar(128) NOT NULL,
      goods_key varchar(128) NOT NULL,
      quota_amount bigint NOT NULL,
      plan_id integer,
      expected_amount_minor bigint NOT NULL,
      currency varchar(8) NOT NULL,
      enabled numeric,
      sort_order integer,
      created_by integer,
      updated_by integer,
      created_at bigint,
      updated_at bigint
    );
    CREATE UNIQUE INDEX idx_liandong_products_goods_key
      ON liandong_products(goods_key);
    CREATE INDEX idx_liandong_products_business_type
      ON liandong_products(business_type);
    CREATE INDEX idx_liandong_products_plan_id
      ON liandong_products(plan_id);

    CREATE TABLE liandong_orders (
      id integer PRIMARY KEY AUTOINCREMENT,
      local_trade_no varchar(128) NOT NULL,
      provider_trade_no varchar(128),
      user_id integer NOT NULL,
      product_id integer NOT NULL,
      product_name_snapshot varchar(128) NOT NULL,
      business_type varchar(32) NOT NULL,
      target_id integer,
      goods_key_snapshot varchar(128) NOT NULL,
      contact_snapshot varchar(12) NOT NULL,
      j_uuid_snapshot varchar(128) NOT NULL,
      expected_amount_minor bigint NOT NULL,
      currency_snapshot varchar(8) NOT NULL,
      fulfillment_snapshot text NOT NULL,
      payment_status varchar(32) NOT NULL,
      fulfillment_status varchar(32) NOT NULL,
      last_check_at bigint,
      next_check_at bigint,
      check_deadline_at bigint,
      check_count integer,
      consecutive_error_count integer,
      check_lock_until bigint,
      provider_summary text,
      last_error text,
      paid_at bigint,
      fulfilled_at bigint,
      created_at bigint,
      updated_at bigint
    );
    CREATE UNIQUE INDEX idx_liandong_orders_local_trade_no
      ON liandong_orders(local_trade_no);
    CREATE UNIQUE INDEX idx_liandong_orders_provider_trade_no
      ON liandong_orders(provider_trade_no);
    CREATE UNIQUE INDEX idx_liandong_orders_contact_snapshot
      ON liandong_orders(contact_snapshot);
    CREATE INDEX idx_liandong_orders_user_id ON liandong_orders(user_id);
    CREATE INDEX idx_liandong_orders_product_id ON liandong_orders(product_id);
    CREATE INDEX idx_liandong_orders_business_type ON liandong_orders(business_type);
    CREATE INDEX idx_liandong_orders_target_id ON liandong_orders(target_id);
    CREATE INDEX idx_liandong_orders_payment_status ON liandong_orders(payment_status);
    CREATE INDEX idx_liandong_orders_fulfillment_status
      ON liandong_orders(fulfillment_status);
    CREATE INDEX idx_liandong_orders_next_check_at ON liandong_orders(next_check_at);
    CREATE INDEX idx_liandong_orders_check_deadline_at
      ON liandong_orders(check_deadline_at);
    CREATE INDEX idx_liandong_orders_check_lock_until
      ON liandong_orders(check_lock_until);
    CREATE INDEX idx_liandong_orders_created_at ON liandong_orders(created_at);

    INSERT INTO liandong_products (
      id, business_type, name, goods_key, quota_amount,
      expected_amount_minor, currency
    ) VALUES (7, 'quota', 'Legacy Product', 'legacy-key', 1000, 1999, 'CNY');
    INSERT INTO liandong_orders (
      id, local_trade_no, user_id, product_id, product_name_snapshot,
      business_type, goods_key_snapshot, contact_snapshot, j_uuid_snapshot,
      expected_amount_minor, currency_snapshot, fulfillment_snapshot,
      payment_status, fulfillment_status
    ) VALUES (
      9, 'LD-E2E', 11, 7, 'Legacy Product', 'quota', 'legacy-key',
      '138001380001', 'juuid', 1999, 'CNY', '{}', 'pending', 'waiting'
    );
    """
)
database.close()
PY

(
  cd -- "${PACKAGE_DIR}"
  find . -type f ! -path './SHA256SUMS' -printf '%P\n' |
    LC_ALL=C sort |
    while IFS= read -r file; do
      sha256sum -- "${file}"
    done >SHA256SUMS
)

PATCH_APP_DIR_VALUE="${APP_DIR}"
PATCH_BACKUP_ROOT_VALUE="${BACKUP_ROOT}"
PATCH_SQLITE_PATH_VALUE="${DATABASE_PATH}"
if [[ "${PLATFORM}" == "windows" ]]; then
  PATCH_APP_DIR_VALUE="${NATIVE_APP_DIR}"
  PATCH_BACKUP_ROOT_VALUE="$(cygpath -w "${BACKUP_ROOT}")"
  PATCH_SQLITE_PATH_VALUE="${NATIVE_DATABASE_PATH}"
fi
export PATCH_APP_DIR="${PATCH_APP_DIR_VALUE}"
export PATCH_BACKUP_ROOT="${PATCH_BACKUP_ROOT_VALUE}"
export PATCH_DB_TYPE=sqlite
export PATCH_SQLITE_PATH="${PATCH_SQLITE_PATH_VALUE}"

identity="$(
  PATCH_DB_TYPE=sqlite \
    PATCH_SQLITE_PATH="${NATIVE_DATABASE_PATH}" \
    "${PATCHDB_BIN}" identity --app-dir "${NATIVE_APP_DIR}"
)"
fingerprint="$(
  printf '%s\n' "${identity}" |
    awk -F= '$1 == "DB_FINGERPRINT" { print substr($0, index($0, "=") + 1) }'
)"
export PATCH_CONFIRM_DEPLOY="new-api-20260716:${fingerprint}"

"${PACKAGE_DIR}/patchctl.sh" deploy \
  --non-interactive \
  --confirm-writers-stopped
"${PACKAGE_DIR}/patchctl.sh" verify --non-interactive

backup_dir="$(tr -d '\r\n' <"${BACKUP_ROOT}/LATEST_DEPLOY")"
backup_database="$(
  sed -n 's/^DATABASE_BACKUP=//p' "${backup_dir}/backup-manifest.env"
)"
NATIVE_BACKUP_DATABASE="${backup_dir}/${backup_database}"
if [[ "${PLATFORM}" == "windows" ]]; then
  NATIVE_BACKUP_DATABASE="$(cygpath -w "${NATIVE_BACKUP_DATABASE}")"
fi
PATCH_DB_TYPE=sqlite \
  PATCH_SQLITE_PATH="${NATIVE_BACKUP_DATABASE}" \
  "${PATCHDB_BIN}" inspect \
  --app-dir "${NATIVE_APP_DIR}" \
  --ddl-dir "${NATIVE_DDL_DIR}" |
  grep -Fqx 'SCHEMA_STATE=legacy'

DATABASE_PATH="${NATIVE_DATABASE_PATH}" python - <<'PY'
import os
import sqlite3

database = sqlite3.connect(os.environ["DATABASE_PATH"])
row = database.execute(
    """
    SELECT local_trade_no, inventory_code_id, expires_at, closed_reason, late_payment
    FROM liandong_orders
    WHERE id = 9
    """
).fetchone()
assert row == ("LD-E2E", 0, 0, "", 0), row
history = database.execute(
    """
    SELECT state, length(ddl_checksum)
    FROM new_api_patch_history
    WHERE patch_id = 'new-api-20260716'
    """
).fetchone()
assert history == ("success", 64), history
database.close()
PY

export PATCH_CONFIRM_ROLLBACK="new-api-20260716:$(basename -- "${backup_dir}")"
ROLLBACK_BACKUP_DIR="${backup_dir}"
if [[ "${PLATFORM}" == "windows" ]]; then
  ROLLBACK_BACKUP_DIR="$(cygpath -w "${backup_dir}")"
fi
"${PACKAGE_DIR}/patchctl.sh" rollback \
  --non-interactive \
  --backup-dir "${ROLLBACK_BACKUP_DIR}"

cmp -s -- "${APP_BINARY}" "${TEST_ROOT}/${BINARY_NAME}.old"
[[ ! -f "${APP_DIR}/.run/new-api.pid" ]]
printf 'PASS: %s real SQLite backup -> deploy -> verify -> rollback\n' "${PLATFORM}"
