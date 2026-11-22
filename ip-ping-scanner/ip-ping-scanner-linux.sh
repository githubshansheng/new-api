#!/usr/bin/env bash

set -u

if ((BASH_VERSINFO[0] < 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] < 3))); then
  printf '错误：需要 Bash 4.3 或更高版本。\n' >&2
  exit 1
fi

DEFAULT_CONCURRENCY=50
DEFAULT_PING_COUNT=5
DEFAULT_TIMEOUT_MS=800
DEFAULT_MAX_TARGETS=1000000
DEFAULT_GEO_CONCURRENCY=5

concurrency=$DEFAULT_CONCURRENCY
ping_count=$DEFAULT_PING_COUNT
timeout_ms=$DEFAULT_TIMEOUT_MS
max_targets=$DEFAULT_MAX_TARGETS
geo_concurrency=$DEFAULT_GEO_CONCURRENCY
pattern_file=""
output_directory=""
geo_csv=""
non_interactive=0
preview_only=0
declare -a direct_patterns=()
declare -a built_in_patterns=(
  "209.*.76.33"
  "209.*.76.187"
  "209.*.76.51"
  "209.*.76.143"
  "209.*.76.20"
  "209.*.76.37"
  "209.*.76.168"
  "209.*.76.191"
  "209.*.76.218"
  "209.*.76.62"
  "209.*.76.72"
  "209.*.76.214"
  "209.*.76.249"
  "209.*.76.222"
  "209.*.76.213"
  "209.*.76.203"
  "209.*.76.121"
  "209.*.76.149"
  "209.*.76.15"
  "209.*.76.175"
  "209.*.76.234"
  "209.*.76.232"
  "209.*.76.41"
  "209.*.76.21"
  "209.*.76.92"
  "209.*.76.151"
  "209.*.76.130"
  "209.*.76.102"
  "209.*.76.139"
  "209.*.76.43"
  "209.*.76.231"
  "209.*.76.28"
  "209.*.76.119"
  "209.*.76.87"
  "209.*.76.195"
  "209.*.76.205"
  "209.*.76.170"
  "209.*.76.24"
  "209.*.76.54"
  "209.*.76.227"
  "209.*.76.162"
  "209.*.76.240"
  "209.*.76.31"
  "209.*.76.147"
  "209.*.76.153"
  "209.*.76.126"
  "209.*.76.3"
  "209.*.76.112"
  "209.*.76.25"
  "209.*.76.140"
  "209.*.76.63"
  "209.*.76.52"
  "209.*.76.206"
  "209.*.76.152"
  "209.*.76.73"
  "209.*.76.117"
  "209.*.76.167"
  "209.*.76.180"
)

declare -a patterns=()
declare -a targets=()
declare -A seen_targets=()
script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

print_help() {
  cat <<'EOF'
用法：
  ./ip-ping-scanner-linux.sh [选项]

选项：
  -c, --concurrency 数量     并发数，默认 50，范围 1–256
  -n, --ping-count 数量      每个 IP 的 Ping 包数，默认 5，范围 1–100
  -t, --timeout-ms 毫秒      单包超时，默认 800，范围 100–10000
  -p, --pattern IP规则       指定 IP 规则，可重复使用；* 或 x 填充为 0–255
  -f, --pattern-file 文件    从文件读取 IP 规则
  -o, --output-dir 目录      输出根目录
      --geo-csv CSV文件      补充已有 CSV 中 IP 的归属地后退出
      --geo-concurrency 数量  归属地查询并发数，默认 5，范围 1–20
      --max-targets 数量     允许展开的最大唯一目标数，默认 1000000
      --preview              只预览展开结果，不执行扫描
      --non-interactive      不显示菜单，直接扫描
  -h, --help                 显示帮助

示例：
  ./ip-ping-scanner-linux.sh
  ./ip-ping-scanner-linux.sh -c 100 -n 3 -t 1000
  ./ip-ping-scanner-linux.sh -p '10.x.20.30' --preview
  ./ip-ping-scanner-linux.sh --geo-csv ./alive.csv
EOF
}

fail() {
  printf '错误：%s\n' "$1" >&2
  exit 1
}

is_integer_in_range() {
  local value=$1
  local minimum=$2
  local maximum=$3
  [[ $value =~ ^[0-9]+$ ]] && ((10#$value >= minimum && 10#$value <= maximum))
}

require_value() {
  local option=$1
  local remaining=$2
  ((remaining >= 2)) || fail "选项 $option 缺少参数。"
}

while (($# > 0)); do
  case "$1" in
    -c|--concurrency)
      require_value "$1" "$#"
      concurrency=$2
      shift 2
      ;;
    -n|--ping-count)
      require_value "$1" "$#"
      ping_count=$2
      shift 2
      ;;
    -t|--timeout-ms)
      require_value "$1" "$#"
      timeout_ms=$2
      shift 2
      ;;
    -p|--pattern)
      require_value "$1" "$#"
      direct_patterns+=("$2")
      shift 2
      ;;
    -f|--pattern-file)
      require_value "$1" "$#"
      pattern_file=$2
      shift 2
      ;;
    -o|--output-dir)
      require_value "$1" "$#"
      output_directory=$2
      shift 2
      ;;
    --geo-csv)
      require_value "$1" "$#"
      geo_csv=$2
      shift 2
      ;;
    --geo-concurrency)
      require_value "$1" "$#"
      geo_concurrency=$2
      shift 2
      ;;
    --max-targets)
      require_value "$1" "$#"
      max_targets=$2
      shift 2
      ;;
    --preview)
      preview_only=1
      shift
      ;;
    --non-interactive)
      non_interactive=1
      shift
      ;;
    -h|--help)
      print_help
      exit 0
      ;;
    --)
      shift
      break
      ;;
    *)
      fail "未知选项：$1"
      ;;
  esac
done

is_integer_in_range "$concurrency" 1 256 || fail "并发数必须是 1–256 之间的整数。"
is_integer_in_range "$ping_count" 1 100 || fail "Ping 包数必须是 1–100 之间的整数。"
is_integer_in_range "$timeout_ms" 100 10000 || fail "超时时间必须是 100–10000 之间的整数。"
is_integer_in_range "$max_targets" 1 1000000 || fail "目标数量上限必须是 1–1000000 之间的整数。"
is_integer_in_range "$geo_concurrency" 1 20 || fail "归属地查询并发数必须是 1–20 之间的整数。"
concurrency=$((10#$concurrency))
ping_count=$((10#$ping_count))
timeout_ms=$((10#$timeout_ms))
max_targets=$((10#$max_targets))
geo_concurrency=$((10#$geo_concurrency))

if [[ -z $output_directory ]]; then
  output_directory="$script_directory/results-linux"
fi

load_patterns() {
  patterns=()
  local line token
  local -a tokens=()
  local -a source_lines=()

  if ((${#direct_patterns[@]} > 0)); then
    source_lines=("${direct_patterns[@]}")
  elif [[ -n $pattern_file ]]; then
    [[ -f $pattern_file ]] || fail "找不到 IP 规则文件：$pattern_file"
    mapfile -t source_lines < "$pattern_file"
  else
    source_lines=("${built_in_patterns[@]}")
  fi

  for line in "${source_lines[@]}"; do
    line=${line%%#*}
    line=${line//,/ }
    line=${line//;/ }
    read -r -a tokens <<< "$line"
    for token in "${tokens[@]}"; do
      [[ -n $token ]] && patterns+=("$token")
    done
  done

  ((${#patterns[@]} > 0)) || fail "没有找到任何 IP 规则。"
}

add_target() {
  local address=$1
  if [[ -z ${seen_targets[$address]+存在} ]]; then
    ((${#targets[@]} < max_targets)) || fail "展开后的唯一目标数超过上限（$max_targets）。"
    seen_targets[$address]=1
    targets+=("$address")
  fi
}

expand_pattern() {
  local pattern=$1
  local first second third fourth extra
  local octet value lower estimated_count=1
  local -a octets=()
  local -a partials=("")
  local -a next_partials=()
  local partial address

  IFS='.' read -r first second third fourth extra <<< "$pattern"
  if [[ -z ${first:-} || -z ${second:-} || -z ${third:-} || -z ${fourth:-} || -n ${extra:-} ]]; then
    fail "IPv4 规则 '$pattern' 无效：必须正好包含四个八位组。"
  fi
  octets=("$first" "$second" "$third" "$fourth")

  for octet in "${octets[@]}"; do
    lower=${octet,,}
    if [[ $octet == '*' || $lower == 'x' ]]; then
      ((estimated_count <= max_targets / 256)) || fail "规则 '$pattern' 展开后超过目标数量上限（$max_targets）。"
      ((estimated_count *= 256))
    elif ! [[ $octet =~ ^[0-9]+$ ]] || ((10#$octet > 255)); then
      fail "规则 '$pattern' 中的八位组 '$octet' 无效，请使用 0–255、* 或 x。"
    fi
  done
  for octet in "${octets[@]}"; do
    lower=${octet,,}
    if [[ $octet == '*' || $lower == 'x' ]]; then
      next_partials=()
      for partial in "${partials[@]}"; do
        for ((value = 0; value <= 255; value++)); do
          if [[ -z $partial ]]; then
            next_partials+=("$value")
          else
            next_partials+=("$partial.$value")
          fi
        done
      done
      partials=("${next_partials[@]}")
      continue
    fi

    if ! [[ $octet =~ ^[0-9]+$ ]] || ((10#$octet > 255)); then
      fail "规则 '$pattern' 中的八位组 '$octet' 无效，请使用 0–255、* 或 x。"
    fi
    value=$((10#$octet))
    next_partials=()
    for partial in "${partials[@]}"; do
      if [[ -z $partial ]]; then
        next_partials+=("$value")
      else
        next_partials+=("$partial.$value")
      fi
    done
    partials=("${next_partials[@]}")
  done

  for address in "${partials[@]}"; do
    add_target "$address"
  done
}

expand_patterns() {
  targets=()
  seen_targets=()
  local pattern
  for pattern in "${patterns[@]}"; do
    expand_pattern "$pattern"
  done
}

show_preview() {
  local preview_count=8
  local index
  ((${#targets[@]} < preview_count)) && preview_count=${#targets[@]}

  printf '\nIP 规则数：%d\n' "${#patterns[@]}"
  printf '展开后的唯一目标数：%d\n' "${#targets[@]}"
  printf '通配规则：* 或 x 自动填充为 0–255\n'
  if ((preview_count > 0)); then
    printf '前 %d 个目标：\n' "$preview_count"
    for ((index = 0; index < preview_count; index++)); do
      printf '  %s\n' "${targets[$index]}"
    done
  fi
  if ((${#targets[@]} > preview_count)); then
    printf '后 %d 个目标：\n' "$preview_count"
    for ((index = ${#targets[@]} - preview_count; index < ${#targets[@]}; index++)); do
      printf '  %s\n' "${targets[$index]}"
    done
  fi
  printf '\n'
}

read_integer_setting() {
  local label=$1
  local current=$2
  local minimum=$3
  local maximum=$4
  local entered

  while true; do
    read -r -p "$label [$current]：" entered || return 1
    if [[ -z $entered ]]; then
      REPLY=$current
      return 0
    fi
    if is_integer_in_range "$entered" "$minimum" "$maximum"; then
      REPLY=$((10#$entered))
      return 0
    fi
    printf '请输入 %d 到 %d 之间的整数。\n' "$minimum" "$maximum"
  done
}

select_csv_file() {
  local selection entered_path
  local -a csv_files=()
  local index search_root
  local -A discovered_csv=()

  for search_root in "$output_directory" "$script_directory"; do
    [[ -d $search_root ]] || continue
    while IFS= read -r -d '' entered_path; do
      if [[ -z ${discovered_csv[$entered_path]+存在} ]]; then
        discovered_csv[$entered_path]=1
        csv_files+=("$entered_path")
      fi
    done < <(find "$search_root" -type f -name '*.csv' -print0 2>/dev/null)
  done

  printf '\n可选择的 CSV 文件：\n'
  if ((${#csv_files[@]} > 0)); then
    for ((index = 0; index < ${#csv_files[@]}; index++)); do
      printf ' %d. %s\n' "$((index + 1))" "${csv_files[$index]}"
    done
  else
    printf ' 当前输出目录中没有找到 CSV 文件。\n'
  fi
  printf ' 0. 手动输入 CSV 路径\n'

  read -r -p '请选择编号，或直接输入 CSV 路径：' selection || return 1
  if [[ $selection =~ ^[0-9]+$ ]]; then
    if ((10#$selection == 0)); then
      read -r -p '请输入 CSV 文件路径：' entered_path || return 1
    elif ((10#$selection <= ${#csv_files[@]})); then
      entered_path=${csv_files[$((10#$selection - 1))]}
    else
      printf '编号超出范围。\n' >&2
      return 1
    fi
  else
    entered_path=$selection
  fi

  [[ -f $entered_path ]] || {
    printf '找不到 CSV 文件：%s\n' "$entered_path" >&2
    return 1
  }
  SELECTED_CSV=$entered_path
}

enrich_csv_geolocation() {
  local input_csv=$1
  local input_directory input_name input_stem output_csv

  [[ -f $input_csv ]] || fail "找不到 CSV 文件：$input_csv"
  command -v python3 >/dev/null 2>&1 || fail "未找到 python3，CSV 归属地核查需要 Python 3。"

  input_directory=$(cd -- "$(dirname -- "$input_csv")" && pwd)
  input_name=$(basename -- "$input_csv")
  input_stem=${input_name%.*}
  output_csv="$input_directory/${input_stem}_geo_$(date '+%Y%m%d_%H%M%S')_$$.csv"

  printf '\n开始核查 CSV 中的 IP 归属地：%s\n' "$input_csv"
  printf '数据源：ipwho.is；查询并发数：%d\n' "$geo_concurrency"
  printf '说明：IP 归属地是数据库推断结果，不代表设备的精确或实时物理位置。\n\n'

  python3 - "$input_csv" "$output_csv" "$geo_concurrency" <<'PYTHON'
import csv
import ipaddress
import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone

input_path, output_path, concurrency_text = sys.argv[1:4]
concurrency = int(concurrency_text)
geo_fields = [
    "geo_status",
    "geo_query_ip",
    "geo_continent",
    "geo_country",
    "geo_country_code",
    "geo_region",
    "geo_region_code",
    "geo_city",
    "geo_postal",
    "geo_latitude",
    "geo_longitude",
    "geo_isp",
    "geo_org",
    "geo_asn",
    "geo_timezone",
    "geo_source",
    "geo_checked_at",
    "geo_error",
]


def checked_at():
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def empty_result(status, query_ip="", error="", source="ipwho.is"):
    result = {field: "" for field in geo_fields}
    result.update(
        geo_status=status,
        geo_query_ip=query_ip,
        geo_source=source,
        geo_checked_at=checked_at(),
        geo_error=error,
    )
    return result


def query_ip(query_ip):
    url = "https://ipwho.is/" + urllib.parse.quote(query_ip, safe="")
    last_error = ""
    for attempt in range(3):
        try:
            request = urllib.request.Request(
                url,
                headers={
                    "Accept": "application/json",
                    "User-Agent": "ip-ping-scanner/1.0",
                },
            )
            with urllib.request.urlopen(request, timeout=12) as response:
                payload = json.load(response)
            if not payload.get("success"):
                return empty_result(
                    "failed",
                    query_ip,
                    str(payload.get("message") or "归属地数据源未返回有效结果"),
                )
            connection = payload.get("connection") or {}
            timezone_data = payload.get("timezone") or {}
            result = empty_result("success", query_ip)
            result.update(
                geo_continent=payload.get("continent", ""),
                geo_country=payload.get("country", ""),
                geo_country_code=payload.get("country_code", ""),
                geo_region=payload.get("region", ""),
                geo_region_code=payload.get("region_code", ""),
                geo_city=payload.get("city", ""),
                geo_postal=payload.get("postal", ""),
                geo_latitude=payload.get("latitude", ""),
                geo_longitude=payload.get("longitude", ""),
                geo_isp=connection.get("isp", ""),
                geo_org=connection.get("org", ""),
                geo_asn=connection.get("asn", ""),
                geo_timezone=timezone_data.get("id", ""),
                geo_error="",
            )
            return result
        except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError, json.JSONDecodeError) as error:
            last_error = str(error)
            if attempt < 2:
                time.sleep(0.75 * (2**attempt))
        except Exception as error:
            last_error = str(error)
            break
    return empty_result("failed", query_ip, last_error or "查询失败")


with open(input_path, "r", encoding="utf-8-sig", newline="") as input_file:
    reader = csv.DictReader(input_file)
    if not reader.fieldnames:
        raise SystemExit("错误：CSV 没有表头。")
    original_fields = list(reader.fieldnames)
    rows = list(reader)

normalized_headers = {
    header.strip().lower().replace("-", "_"): header for header in original_fields
}
ip_column = next(
    (
        normalized_headers[name]
        for name in ("ip", "ip_address", "address", "reply_address")
        if name in normalized_headers
    ),
    None,
)
if ip_column is None:
    raise SystemExit(
        "错误：CSV 中未找到 IP 列；支持的表头为 ip、ip_address、address、reply_address。"
    )

row_keys = []
public_ips = []
seen_ips = set()
fixed_results = {}
for row_number, row in enumerate(rows, start=2):
    raw_ip = (row.get(ip_column) or "").strip()
    if not raw_ip:
        key = ("row", row_number)
        row_keys.append(key)
        fixed_results[key] = empty_result(
            "empty", error="IP 为空", source="local-validation"
        )
        continue
    try:
        address = ipaddress.ip_address(raw_ip)
        normalized_ip = address.compressed
    except ValueError:
        key = ("row", row_number)
        row_keys.append(key)
        fixed_results[key] = empty_result(
            "invalid", raw_ip, "IP 格式无效", source="local-validation"
        )
        continue
    if not address.is_global:
        key = ("ip", normalized_ip)
        row_keys.append(key)
        fixed_results[key] = empty_result(
            "not_public",
            normalized_ip,
            "非公网 IP，不查询公网归属地",
            source="local-validation",
        )
        continue
    key = ("ip", normalized_ip)
    row_keys.append(key)
    if normalized_ip not in seen_ips:
        seen_ips.add(normalized_ip)
        public_ips.append(normalized_ip)

lookup_results = {}
total = len(public_ips)
if total:
    with ThreadPoolExecutor(max_workers=concurrency) as executor:
        future_to_ip = {executor.submit(query_ip, ip): ip for ip in public_ips}
        for completed, future in enumerate(as_completed(future_to_ip), start=1):
            query_ip_value = future_to_ip[future]
            try:
                lookup_results[("ip", query_ip_value)] = future.result()
            except Exception as error:
                lookup_results[("ip", query_ip_value)] = empty_result(
                    "failed", query_ip_value, str(error)
                )
            print(
                f"\r归属地查询进度：{completed} / {total}",
                end="",
                flush=True,
            )
    print()
else:
    print("没有需要提交到归属地数据源的公网 IP。")

output_fields = original_fields + [
    field for field in geo_fields if field not in original_fields
]
status_counts = {}
with open(output_path, "w", encoding="utf-8-sig", newline="") as output_file:
    writer = csv.DictWriter(output_file, fieldnames=output_fields, extrasaction="ignore")
    writer.writeheader()
    for row, key in zip(rows, row_keys):
        result = fixed_results.get(key) or lookup_results.get(key)
        if result is None:
            result = empty_result("failed", error="缺少查询结果")
        row.update(result)
        status = result["geo_status"]
        status_counts[status] = status_counts.get(status, 0) + 1
        writer.writerow(row)

print(f"CSV 总行数：{len(rows)}")
print(f"唯一公网 IP：{len(public_ips)}")
for status in sorted(status_counts):
    print(f"{status}：{status_counts[status]}")
print(f"归属地补充结果：{output_path}")
PYTHON

  local python_exit_code=$?
  ((python_exit_code == 0)) || {
    rm -f -- "$output_csv"
    fail "CSV 归属地核查失败。"
  }
  printf '\n已生成新的 CSV，原文件未被修改：%s\n' "$output_csv"
}

parse_ping_output() {
  local output=$1
  local sent received loss min_latency average_latency maximum_latency
  local packet_line rtt_line

  packet_line=$(printf '%s\n' "$output" | LC_ALL=C awk '/packets transmitted/ {line=$0} END {print line}')
  rtt_line=$(printf '%s\n' "$output" | LC_ALL=C awk -F'= ' '/^rtt |^round-trip / {line=$2} END {print line}')
  sent=$(printf '%s\n' "$packet_line" | LC_ALL=C awk '{print $1}')
  received=$(printf '%s\n' "$packet_line" | LC_ALL=C awk -F', ' '{for (i = 1; i <= NF; i++) if ($i ~ / received/) {split($i, value, " "); print value[1]}}')
  loss=$(printf '%s\n' "$packet_line" | LC_ALL=C awk -F', ' '{for (i = 1; i <= NF; i++) if ($i ~ /packet loss/) {split($i, value, "%"); sub(/^.* /, "", value[1]); print value[1]}}')
  sent=${sent:-$ping_count}
  received=${received:-0}
  loss=${loss:-100}
  min_latency=""
  average_latency=""
  maximum_latency=""
  if [[ -n $rtt_line ]]; then
    IFS='/' read -r min_latency average_latency maximum_latency _ <<< "$rtt_line"
  fi
  printf '%s,%s,%s,%s,%s,%s\n' "$sent" "$received" "$loss" "$min_latency" "$average_latency" "$maximum_latency"
}

ping_one_target() {
  local address=$1
  local result_file=$2
  local ping_output stats sent received loss min_latency average_latency maximum_latency scanned_at
  local timeout_seconds

  timeout_seconds=$(awk -v milliseconds="$timeout_ms" 'BEGIN {printf "%.3f", milliseconds / 1000}')
  ping_output=$(LC_ALL=C ping -n -c "$ping_count" -W "$timeout_seconds" "$address" 2>&1 || true)
  stats=$(parse_ping_output "$ping_output")
  IFS=',' read -r sent received loss min_latency average_latency maximum_latency <<< "$stats"
  if ((received > 0)); then
    scanned_at=$(date --iso-8601=seconds 2>/dev/null || date '+%Y-%m-%dT%H:%M:%S%z')
    printf '%s,%s,%s,%s,%s,%s,%s,%s\n' \
      "$address" "$sent" "$received" "$loss" "$min_latency" "$average_latency" "$maximum_latency" "$scanned_at" \
      > "$result_file"
  else
    : > "$result_file"
  fi
}

run_scan() {
  command -v ping >/dev/null 2>&1 || fail "未找到 ping 命令，请先安装 iputils-ping。"
  command -v awk >/dev/null 2>&1 || fail "未找到 awk 命令。"
  ((${#targets[@]} > 0)) || fail "没有可以扫描的目标。"

  local started_at finished_at run_name run_directory temporary_directory
  local alive_text_path alive_csv_path summary_path result_file line
  local index=0 processed=0 alive_count=0
  local total_probes=$(( ${#targets[@]} * ping_count ))
  local -a process_ids=()
  local process_id

  started_at=$(date --iso-8601=seconds 2>/dev/null || date '+%Y-%m-%dT%H:%M:%S%z')
  run_name="ping_scan_$(date '+%Y%m%d_%H%M%S')_$$"
  run_directory="$output_directory/$run_name"
  temporary_directory="$run_directory/.tmp"
  mkdir -p -- "$temporary_directory" || fail "无法创建输出目录：$run_directory"
  alive_text_path="$run_directory/alive.txt"
  alive_csv_path="$run_directory/alive.csv"
  summary_path="$run_directory/summary.txt"
  : > "$alive_text_path"
  printf 'ip,packets_sent,packets_received,packet_loss_percent,min_ms,avg_ms,max_ms,scanned_at\n' > "$alive_csv_path"

  printf '\n开始扫描 %d 个目标，每个目标 %d 个 Ping 包（共 %d 次探测），并发数 %d……\n' \
    "${#targets[@]}" "$ping_count" "$total_probes" "$concurrency"
  printf '结果实时保存到：%s\n' "$run_directory"
  printf '按 Ctrl+C 可停止扫描，已经写入的结果会保留。\n\n'

  cleanup_scan() {
    local pid
    for pid in "${process_ids[@]}"; do
      kill "$pid" 2>/dev/null || true
    done
    wait 2>/dev/null || true
    rm -rf -- "$temporary_directory"
    printf '\n扫描已中止，已经导出的结果保留在：%s\n' "$run_directory"
    exit 130
  }
  trap cleanup_scan INT TERM

  while ((index < ${#targets[@]} || ${#process_ids[@]} > 0)); do
    while ((index < ${#targets[@]} && ${#process_ids[@]} < concurrency)); do
      result_file="$temporary_directory/$index.csv"
      ping_one_target "${targets[$index]}" "$result_file" &
      process_ids+=("$!")
      ((index++))
    done

    wait -n 2>/dev/null || true
    local -a running_ids=()
    for process_id in "${process_ids[@]}"; do
      if kill -0 "$process_id" 2>/dev/null; then
        running_ids+=("$process_id")
      fi
    done
    process_ids=("${running_ids[@]}")

    while ((processed < index)) && [[ -f "$temporary_directory/$processed.csv" ]]; do
      result_file="$temporary_directory/$processed.csv"
      if [[ -s $result_file ]]; then
        line=$(<"$result_file")
        printf '%s\n' "$line" >> "$alive_csv_path"
        printf '%s\n' "${line%%,*}" >> "$alive_text_path"
        ((alive_count++))
        IFS=',' read -r address sent received loss min_latency average_latency maximum_latency _ <<< "$line"
        printf '[在线] %-15s 响应 %s/%s，平均 %s ms，丢包率 %s%%\n' \
          "$address" "$received" "$sent" "$average_latency" "$loss"
      fi
      rm -f -- "$result_file"
      ((processed++))
      printf '\r进度：%d / %d，可达 %d' "$processed" "${#targets[@]}" "$alive_count"
    done
  done

  trap - INT TERM
  rm -rf -- "$temporary_directory"
  finished_at=$(date --iso-8601=seconds 2>/dev/null || date '+%Y-%m-%dT%H:%M:%S%z')
  {
    printf '开始时间：%s\n' "$started_at"
    printf '完成时间：%s\n' "$finished_at"
    printf 'IP 规则数：%d\n' "${#patterns[@]}"
    printf '展开后的唯一目标数：%d\n' "${#targets[@]}"
    printf '可达目标数：%d\n' "$alive_count"
    printf '无响应或出错数：%d\n' "$(( ${#targets[@]} - alive_count ))"
    printf '每个目标的 Ping 包数：%d\n' "$ping_count"
    printf '单包超时（毫秒）：%d\n' "$timeout_ms"
    printf '并发数：%d\n' "$concurrency"
    printf '可达结果 TXT：%s\n' "$alive_text_path"
    printf '可达结果 CSV：%s\n' "$alive_csv_path"
  } > "$summary_path"

  printf '\n\n扫描完成，可达目标：%d / %d\n' "$alive_count" "${#targets[@]}"
  printf 'TXT：%s\n' "$alive_text_path"
  printf 'CSV：%s\n' "$alive_csv_path"
  printf '扫描摘要：%s\n' "$summary_path"
  printf '提示：没有 ICMP 响应并不一定表示主机离线。\n\n'
}

if [[ -n $geo_csv ]]; then
  enrich_csv_geolocation "$geo_csv"
  exit 0
fi

load_patterns
expand_patterns

if ((preview_only)); then
  show_preview
  exit 0
fi

if ((non_interactive)); then
  run_scan
  exit 0
fi

while true; do
  if ((${#direct_patterns[@]} > 0)); then
    pattern_source="命令行传入的规则"
  elif [[ -n $pattern_file ]]; then
    pattern_source=$pattern_file
  else
    pattern_source="脚本内置的截图规则"
  fi

  printf '\n============================================================\n'
  printf ' Linux IP 通配规则 Ping 扫描器\n'
  printf '============================================================\n'
  printf ' * 或 x 会自动填充为 0–255。\n'
  printf ' IP 规则来源：%s\n' "$pattern_source"
  printf ' 并发数：%d\n' "$concurrency"
  printf ' Ping 次数：每个目标 %d 个包\n' "$ping_count"
  printf ' 单包超时：%d 毫秒\n' "$timeout_ms"
  printf ' 归属地查询并发数：%d\n' "$geo_concurrency"
  printf ' 输出目录：%s\n' "$output_directory"
  printf '%s\n' '------------------------------------------------------------'
  printf ' 1. 开始扫描并导出可达 IP\n'
  printf ' 2. 预览通配符展开结果\n'
  printf ' 3. 选择已有 CSV 并补充 IP 归属地\n'
  printf ' 4. 修改扫描设置\n'
  printf ' 5. 退出\n'
  printf '%s\n' '------------------------------------------------------------'

  read -r -p '请选择操作：' choice || exit 0
  choice=${choice//[[:space:]]/}
  case "$choice" in
    1)
      load_patterns
      expand_patterns
      show_preview
      read -r -p '请仅扫描已获授权的目标，是否继续？[y/N]：' confirmation || continue
      case "${confirmation,,}" in
        y|yes|是|确认) run_scan ;;
        *) printf '已取消扫描。\n' ;;
      esac
      read -r -p '按 Enter 键返回菜单' _ || exit 0
      ;;
    2)
      load_patterns
      expand_patterns
      show_preview
      read -r -p '按 Enter 键返回菜单' _ || exit 0
      ;;
    3)
      if select_csv_file; then
        enrich_csv_geolocation "$SELECTED_CSV"
      fi
      read -r -p '按 Enter 键返回菜单' _ || exit 0
      ;;
    4)
      read_integer_setting '并发数' "$concurrency" 1 256 || exit 0
      concurrency=$REPLY
      read_integer_setting '每个目标发送的 Ping 包数' "$ping_count" 1 100 || exit 0
      ping_count=$REPLY
      read_integer_setting '单个 Ping 包的超时时间（毫秒）' "$timeout_ms" 100 10000 || exit 0
      timeout_ms=$REPLY
      read_integer_setting '归属地查询并发数' "$geo_concurrency" 1 20 || exit 0
      geo_concurrency=$REPLY

      if ((${#direct_patterns[@]} == 0)); then
        read -r -p "可选 IP 规则文件 [$pattern_source]（输入“内置”恢复截图规则）：" new_pattern_file || exit 0
        if [[ $new_pattern_file == '内置' || ${new_pattern_file^^} == 'BUILTIN' ]]; then
          pattern_file=""
        elif [[ -n $new_pattern_file ]]; then
          pattern_file=$new_pattern_file
        fi
      fi

      read -r -p "输出目录 [$output_directory]：" new_output_directory || exit 0
      [[ -n $new_output_directory ]] && output_directory=$new_output_directory
      load_patterns
      expand_patterns
      ;;
    5)
      exit 0
      ;;
    *)
      printf '请输入 1、2、3、4 或 5。\n'
      ;;
  esac
done
