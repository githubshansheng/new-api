#!/usr/bin/env bash

set -Eeuo pipefail

DEFAULT_DATABASE_PATH="./one-api.db"
DEFAULT_NEW_API_ADMIN_USERNAME="root"
DEFAULT_NEW_API_ADMIN_PASSWORD="123456"

BASE_RECLAIM_COLUMNS=(
  reclaim_status
  reclaim_remaining_quota
  reclaimed_quota
  reclaim_enabled_time
  reclaimed_time
  reclaim_error
)
REVIEW_COLUMNS=(
  reclaim_reviewed_by
  reclaim_reviewed_time
  reclaim_review_note
)
INTEGER_COLUMNS=(
  reclaim_status
  reclaim_remaining_quota
  reclaimed_quota
  reclaim_enabled_time
  reclaimed_time
  reclaim_reviewed_by
  reclaim_reviewed_time
)
TEXT_COLUMNS=(
  reclaim_error
  reclaim_review_note
)
INDEX_NAMES=(
  idx_redemption_reclaim_due
  idx_redemption_reclaim_user
  idx_redemption_reclaim_completed
)
INDEX_COLUMN_LISTS=(
  "reclaim_status,expired_time"
  "used_user_id,reclaim_status"
  "reclaim_status,reclaimed_time"
)

database=""
check_only=0
assume_yes=0
create_backup=1
timeout_seconds=30
timeout_milliseconds=30000
action=""
missing_indexes=()

die() {
  printf '升级失败：%s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
用法：
  bash apply_limited_quota_reclaim_sqlite.sh [数据库文件] [选项]

选项：
  --database PATH  指定 SQLite 数据库文件
  --check-only     只检查结构，不备份、不修改
  --yes            跳过交互确认并执行
  --no-backup      执行升级但不创建备份（不推荐）
  --timeout SEC    等待数据库写锁的秒数，默认 30
  -h, --help       显示帮助

未指定数据库文件时，交互模式会提示输入，默认 ./one-api.db。
EOF
}

while (($# > 0)); do
  case "$1" in
    --database)
      (($# >= 2)) || die "--database 缺少路径"
      database=$2
      shift 2
      ;;
    --check-only)
      check_only=1
      shift
      ;;
    --yes)
      assume_yes=1
      shift
      ;;
    --no-backup)
      create_backup=0
      shift
      ;;
    --timeout)
      (($# >= 2)) || die "--timeout 缺少秒数"
      timeout_seconds=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      break
      ;;
    -* )
      die "未知选项：$1"
      ;;
    *)
      [[ -z "$database" ]] || die "只能指定一个数据库文件"
      database=$1
      shift
      ;;
  esac
done

if (($# > 0)); then
  [[ -z "$database" && $# -eq 1 ]] || die "参数过多"
  database=$1
fi

[[ "$timeout_seconds" =~ ^[1-9][0-9]*$ ]] || die "--timeout 必须是大于 0 的整数"
timeout_milliseconds=$((timeout_seconds * 1000))

command -v sqlite3 >/dev/null 2>&1 || die "未找到 sqlite3，请先安装 SQLite CLI"

if [[ -z "$database" ]]; then
  if [[ -t 0 ]]; then
    read -r -p "SQLite 数据库文件 [${DEFAULT_DATABASE_PATH}]：" database
  fi
  database=${database:-$DEFAULT_DATABASE_PATH}
fi

[[ -f "$database" ]] || die "数据库文件不存在：$database"
database_directory=$(cd -- "$(dirname -- "$database")" && pwd -P)
database="${database_directory}/$(basename -- "$database")"

script_directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
full_sql="${script_directory}/limited-quota-reclaim.sqlite.sql"
review_sql="${script_directory}/limited-quota-reclaim-review.sqlite.sql"
[[ -f "$full_sql" ]] || die "找不到完整升级 DDL：$full_sql"
[[ -f "$review_sql" ]] || die "找不到人工核对升级 DDL：$review_sql"

sql_scalar() {
  sqlite3 \
    -batch \
    -noheader \
    -cmd ".timeout ${timeout_milliseconds}" \
    "$database" \
    "$1"
}

sql_list() {
  local joined=""
  local value
  for value in "$@"; do
    if [[ -n "$joined" ]]; then
      joined+=","
    fi
    joined+="'${value}'"
  done
  printf '%s' "$joined"
}

validate_existing_columns() {
  local names_sql=$1
  local integer_names_sql
  local text_names_sql
  integer_names_sql=$(sql_list "${INTEGER_COLUMNS[@]}")
  text_names_sql=$(sql_list "${TEXT_COLUMNS[@]}")

  local problems
  problems=$(sql_scalar "
    SELECT COALESCE(group_concat(problem, char(10)), '')
    FROM (
      SELECT
        CASE
          WHEN name IN (${integer_names_sql}) THEN
            CASE
              WHEN upper(type) NOT LIKE '%INT%' THEN name || ' 类型应为 INTEGER，实际为 ' || type
              WHEN \"notnull\" != 1 THEN name || ' 应为 NOT NULL'
              WHEN replace(replace(replace(replace(trim(COALESCE(dflt_value, '')), '(', ''), ')', ''), '''', ''), char(34), '') != '0'
                THEN name || ' 默认值应为 0，实际为 ' || COALESCE(dflt_value, 'NULL')
            END
          WHEN name IN (${text_names_sql}) THEN
            CASE
              WHEN NOT (
                upper(type) LIKE '%CHAR%'
                OR upper(type) LIKE '%CLOB%'
                OR upper(type) LIKE '%TEXT%'
              ) THEN name || ' 类型应为 TEXT，实际为 ' || type
              WHEN \"notnull\" != 0 THEN name || ' 应允许 NULL'
              WHEN dflt_value IS NOT NULL THEN name || ' 不应设置数据库默认值'
            END
        END AS problem
      FROM pragma_table_info('redemptions')
      WHERE name IN (${names_sql})
    )
    WHERE problem IS NOT NULL;
  ")
  [[ -z "$problems" ]] || die $'检测到不兼容的回收字段结构：\n'"$problems"
}

inspect_indexes() {
  missing_indexes=()
  local index
  local expected_columns
  local exists
  local metadata
  local actual_columns
  local position

  for position in "${!INDEX_NAMES[@]}"; do
    index=${INDEX_NAMES[$position]}
    expected_columns=${INDEX_COLUMN_LISTS[$position]}
    exists=$(sql_scalar "
      SELECT COUNT(*)
      FROM pragma_index_list('redemptions')
      WHERE name = '${index}';
    ")
    if [[ "$exists" == "0" ]]; then
      missing_indexes+=("$index")
      continue
    fi

    metadata=$(sql_scalar "
      SELECT \"unique\" || ',' || partial
      FROM pragma_index_list('redemptions')
      WHERE name = '${index}';
    ")
    actual_columns=$(sql_scalar "
      SELECT COALESCE(group_concat(name, ','), '')
      FROM (
        SELECT name
        FROM pragma_index_info('${index}')
        ORDER BY seqno
      );
    ")
    if [[ "$metadata" != "0,0" || "$actual_columns" != "$expected_columns" ]]; then
      die "索引 ${index} 定义不兼容：期望 ${expected_columns}，实际 ${actual_columns}"
    fi
  done
}

inspect_schema() {
  local table_exists
  local required_existing_count
  local all_names_sql
  local base_names_sql
  local review_names_sql
  local present_count
  local base_count
  local review_count
  local existing_names
  local missing_names

  table_exists=$(sql_scalar "
    SELECT COUNT(*)
    FROM sqlite_master
    WHERE type = 'table' AND name = 'redemptions';
  ")
  [[ "$table_exists" == "1" ]] || die "找不到 redemptions 表，请确认数据库文件属于 New API"

  required_existing_count=$(sql_scalar "
    SELECT COUNT(*)
    FROM pragma_table_info('redemptions')
    WHERE name IN ('used_user_id', 'expired_time');
  ")
  [[ "$required_existing_count" == "2" ]] || die "redemptions 表缺少 used_user_id 或 expired_time 前置字段"

  all_names_sql=$(sql_list "${BASE_RECLAIM_COLUMNS[@]}" "${REVIEW_COLUMNS[@]}")
  base_names_sql=$(sql_list "${BASE_RECLAIM_COLUMNS[@]}")
  review_names_sql=$(sql_list "${REVIEW_COLUMNS[@]}")
  present_count=$(sql_scalar "
    SELECT COUNT(*) FROM pragma_table_info('redemptions')
    WHERE name IN (${all_names_sql});
  ")

  if [[ "$present_count" == "0" ]]; then
    inspect_indexes
    action="full"
    return
  fi

  validate_existing_columns "$all_names_sql"
  base_count=$(sql_scalar "
    SELECT COUNT(*) FROM pragma_table_info('redemptions')
    WHERE name IN (${base_names_sql});
  ")
  review_count=$(sql_scalar "
    SELECT COUNT(*) FROM pragma_table_info('redemptions')
    WHERE name IN (${review_names_sql});
  ")

  if [[ "$base_count" == "6" && "$review_count" == "0" && "$present_count" == "6" ]]; then
    inspect_indexes
    action="review"
    return
  fi

  if [[ "$base_count" == "6" && "$review_count" == "3" && "$present_count" == "9" ]]; then
    inspect_indexes
    if ((${#missing_indexes[@]} > 0)); then
      action="indexes"
    else
      action="complete"
    fi
    return
  fi

  existing_names=$(sql_scalar "
    SELECT COALESCE(group_concat(name, ', '), '无')
    FROM (
      SELECT name FROM pragma_table_info('redemptions')
      WHERE name IN (${all_names_sql})
      ORDER BY name
    );
  ")
  missing_names=$(sql_scalar "
    WITH expected(name) AS (
      VALUES
        ('reclaim_status'),
        ('reclaim_remaining_quota'),
        ('reclaimed_quota'),
        ('reclaim_enabled_time'),
        ('reclaimed_time'),
        ('reclaim_error'),
        ('reclaim_reviewed_by'),
        ('reclaim_reviewed_time'),
        ('reclaim_review_note')
    )
    SELECT COALESCE(group_concat(name, ', '), '无')
    FROM expected
    WHERE name NOT IN (SELECT name FROM pragma_table_info('redemptions'));
  ")
  die "检测到不完整的限时额度回收字段，拒绝自动修改；已存在：${existing_names}；缺少：${missing_names}"
}

describe_action() {
  case "$action" in
    full) printf '未检测到回收字段，需要执行完整升级' ;;
    review) printf '已存在首版回收字段，需要补充人工核对字段' ;;
    indexes) printf '字段已经完整，但需要补齐回收索引' ;;
    complete) printf '字段和索引已经完整，无需重复升级' ;;
    *) die "未知迁移状态：$action" ;;
  esac
}

dot_command_quote() {
  local value=$1
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  printf '"%s"' "$value"
}

backup_database() {
  local timestamp
  local backup_path
  local quoted_backup_path
  timestamp=$(date -u +%Y%m%d-%H%M%SZ)
  backup_path="${database}.backup-${timestamp}-$$"
  [[ ! -e "$backup_path" ]] || die "备份文件已存在：$backup_path"
  quoted_backup_path=$(dot_command_quote "$backup_path")
  sqlite3 \
    -batch \
    -bail \
    -cmd ".timeout ${timeout_milliseconds}" \
    "$database" \
    ".backup ${quoted_backup_path}"
  [[ -s "$backup_path" ]] || die "数据库备份失败：$backup_path"
  printf '%s' "$backup_path"
}

apply_sql_file() {
  local file=$1
  sqlite3 \
    -batch \
    -bail \
    -cmd ".timeout ${timeout_milliseconds}" \
    "$database" < "$file"
}

finish_schema() {
  sqlite3 \
    -batch \
    -bail \
    -cmd ".timeout ${timeout_milliseconds}" \
    "$database" <<'SQL'
BEGIN IMMEDIATE;

UPDATE "redemptions"
SET
  "reclaim_error" = COALESCE("reclaim_error", ''),
  "reclaim_review_note" = COALESCE("reclaim_review_note", '')
WHERE "reclaim_error" IS NULL OR "reclaim_review_note" IS NULL;

CREATE INDEX IF NOT EXISTS "idx_redemption_reclaim_due"
  ON "redemptions" ("reclaim_status", "expired_time");
CREATE INDEX IF NOT EXISTS "idx_redemption_reclaim_user"
  ON "redemptions" ("used_user_id", "reclaim_status");
CREATE INDEX IF NOT EXISTS "idx_redemption_reclaim_completed"
  ON "redemptions" ("reclaim_status", "reclaimed_time");

COMMIT;
SQL
}

validate_target() {
  local all_names_sql
  local target_count
  local null_count
  local quick_check
  all_names_sql=$(sql_list "${BASE_RECLAIM_COLUMNS[@]}" "${REVIEW_COLUMNS[@]}")
  target_count=$(sql_scalar "
    SELECT COUNT(*) FROM pragma_table_info('redemptions')
    WHERE name IN (${all_names_sql});
  ")
  [[ "$target_count" == "9" ]] || die "迁移后回收字段数量不是 9"
  validate_existing_columns "$all_names_sql"
  inspect_indexes
  ((${#missing_indexes[@]} == 0)) || die "迁移后仍缺少索引：${missing_indexes[*]}"

  null_count=$(sql_scalar "
    SELECT COUNT(*)
    FROM redemptions
    WHERE reclaim_status IS NULL
      OR reclaim_remaining_quota IS NULL
      OR reclaimed_quota IS NULL
      OR reclaim_enabled_time IS NULL
      OR reclaimed_time IS NULL
      OR reclaim_reviewed_by IS NULL
      OR reclaim_reviewed_time IS NULL;
  ")
  [[ "$null_count" == "0" ]] || die "迁移后仍有 ${null_count} 条记录包含空的数值回收字段"
  quick_check=$(sql_scalar "PRAGMA quick_check;")
  [[ "$quick_check" == "ok" ]] || die "SQLite quick_check 未通过：$quick_check"
}

printf 'New API 空数据库首次初始化的默认管理员：%s / %s\n' \
  "$DEFAULT_NEW_API_ADMIN_USERNAME" \
  "$DEFAULT_NEW_API_ADMIN_PASSWORD"
printf '%s\n' '说明：这是应用登录账号，不是 SQLite 账号；已有系统的密码可能已经修改。'
printf '%s\n' '安全提示：如果系统仍使用默认密码，请登录后立即修改。'

inspect_schema
printf '数据库：%s\n' "$database"
printf '结构判断：%s\n' "$(describe_action)"
if ((${#missing_indexes[@]} > 0)); then
  printf '缺少索引：%s\n' "${missing_indexes[*]}"
fi

if ((check_only)); then
  if [[ "$action" == "complete" ]]; then
    validate_target
    printf '%s\n' '检查结果：目标字段和索引结构正确'
  else
    printf '%s\n' '检查结果：需要升级，未修改数据库'
  fi
  exit 0
fi

if [[ "$action" == "complete" ]]; then
  validate_target
  printf '%s\n' '执行结果：数据库已经是目标结构，本次幂等跳过'
  exit 0
fi

if ((!assume_yes)); then
  [[ -t 0 ]] || die "非交互环境请使用 --yes，或使用 --check-only 只检查"
  printf '\n请选择操作：\n'
  printf '  1) 创建备份并升级（推荐）\n'
  printf '  2) 只检查并退出\n'
  printf '  3) 不备份直接升级\n'
  printf '  0) 取消\n'
  read -r -p '请输入选项 [1]：' choice
  choice=${choice:-1}
  case "$choice" in
    1) create_backup=1 ;;
    2) printf '%s\n' '未修改数据库'; exit 0 ;;
    3) create_backup=0 ;;
    0) printf '%s\n' '已取消'; exit 0 ;;
    *) die "无效选项：$choice" ;;
  esac
fi

if ((create_backup)); then
  backup_path=$(backup_database)
  printf '备份文件：%s\n' "$backup_path"
else
  printf '%s\n' '警告：已选择不创建备份'
fi

case "$action" in
  full) apply_sql_file "$full_sql" ;;
  review) apply_sql_file "$review_sql" ;;
  indexes) ;;
  *) die "不可执行的迁移状态：$action" ;;
esac

finish_schema
validate_target
printf '%s\n' '执行结果：升级成功；再次运行将直接跳过'
