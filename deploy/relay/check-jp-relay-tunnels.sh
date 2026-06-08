#!/usr/bin/env bash
set -euo pipefail

TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-15}"
OPENAI_PROBE_URL="${OPENAI_PROBE_URL:-https://chatgpt.com/backend-api/codex/responses}"
IP_PROBE_URL="${IP_PROBE_URL:-https://api.ipify.org}"
LOG_FILE="${LOG_FILE:-/var/log/jp-relay-watchdog.log}"
STATE_DIR="${STATE_DIR:-/run/jp-relay-watchdog}"
FAIL_THRESHOLD="${FAIL_THRESHOLD:-3}"
BUSY_DEFER_THRESHOLD="${BUSY_DEFER_THRESHOLD:-20}"
RECENT_CONN_SECONDS="${RECENT_CONN_SECONDS:-45}"

services=(
  "jp-relay-1-tunnel.service|20081|141.11.138.77"
  "jp-relay-2-tunnel.service|20082|141.11.139.81"
  "jp-relay-3-tunnel.service|20083|141.11.138.243"
)

mkdir -p "$STATE_DIR"

log() {
  logger -t jp-relay-watchdog "$1"
  printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')" "$1" >> "$LOG_FILE"
  echo "$1"
}

state_file() {
  local service="$1"
  printf '%s/%s.state\n' "$STATE_DIR" "${service//[^A-Za-z0-9_.-]/_}"
}

read_state_value() {
  local file="$1" key="$2" default="$3"
  if [[ -f "$file" ]]; then
    awk -F= -v k="$key" -v d="$default" '$1==k {print $2; found=1} END {if (!found) print d}' "$file"
  else
    printf '%s\n' "$default"
  fi
}

write_state() {
  local file="$1" fail_count="$2" defer_count="$3" last_failure_ts="$4"
  cat > "$file" <<EOF
fail_count=$fail_count
defer_count=$defer_count
last_failure_ts=$last_failure_ts
EOF
}

clear_state() {
  local file="$1"
  rm -f "$file"
}

probe_openai() {
  local port="$1"
  curl --head --silent --show-error --max-time "$TIMEOUT_SECONDS" \
    --socks5-hostname "127.0.0.1:${port}" \
    "$OPENAI_PROBE_URL" | head -n 1
}

probe_ip() {
  local port="$1"
  curl --silent --show-error --max-time "$TIMEOUT_SECONDS" \
    --socks5-hostname "127.0.0.1:${port}" \
    "$IP_PROBE_URL"
}

active_connection_count() {
  local port="$1"
  ss -tnp 2>/dev/null | awk -v p=":${port}" '
    $1 ~ /^(ESTAB|CLOSE-WAIT|FIN-WAIT-1|FIN-WAIT-2|SYN-SENT)$/ && ($4 ~ p || $5 ~ p) {count++}
    END {print count+0}
  '
}

recent_connection_seen() {
  local port="$1" recent_file="$STATE_DIR/port-${port}.last_active" now
  now="$(date +%s)"
  local active_count
  active_count="$(active_connection_count "$port")"
  if [[ "$active_count" -gt 0 ]]; then
    printf '%s\n' "$now" > "$recent_file"
    return 0
  fi
  if [[ -f "$recent_file" ]]; then
    local last
    last="$(cat "$recent_file" 2>/dev/null || echo 0)"
    if [[ "$last" =~ ^[0-9]+$ ]] && (( now - last <= RECENT_CONN_SECONDS )); then
      return 0
    fi
  fi
  return 1
}

restart_tunnel() {
  local service="$1" port="$2"
  log "$service restarting tunnel on port $port after sustained probe failures"
  systemctl restart "$service"
  sleep 3
}

for entry in "${services[@]}"; do
  IFS='|' read -r service port expected_ip <<<"$entry"
  sf="$(state_file "$service")"

  openai_status=""
  if openai_status="$(probe_openai "$port" 2>/dev/null)" && [[ "$openai_status" == HTTP/* ]]; then
    if [[ -f "$sf" ]]; then
      log "$service probe recovered on port $port with '$openai_status'; clearing failure state"
    fi
    clear_state "$sf"
    continue
  fi

  current_ip=""
  ip_ok=0
  if current_ip="$(probe_ip "$port" 2>/dev/null)" && [[ "$current_ip" == "$expected_ip" ]]; then
    ip_ok=1
  fi

  fail_count="$(read_state_value "$sf" fail_count 0)"
  defer_count="$(read_state_value "$sf" defer_count 0)"
  [[ "$fail_count" =~ ^[0-9]+$ ]] || fail_count=0
  [[ "$defer_count" =~ ^[0-9]+$ ]] || defer_count=0
  fail_count=$((fail_count + 1))
  last_failure_ts="$(date +%s)"

  if (( fail_count < FAIL_THRESHOLD )); then
    if (( ip_ok == 1 )); then
      log "$service probe miss $fail_count/$FAIL_THRESHOLD on port $port; egress IP OK ($current_ip), no restart yet"
    else
      log "$service probe miss $fail_count/$FAIL_THRESHOLD on port $port (openai='${openai_status:-fail}', ip='${current_ip:-fail}', expected='$expected_ip'), no restart yet"
    fi
    write_state "$sf" "$fail_count" "$defer_count" "$last_failure_ts"
    continue
  fi

  if recent_connection_seen "$port" && (( defer_count < BUSY_DEFER_THRESHOLD )); then
    active_count="$(active_connection_count "$port")"
    defer_count=$((defer_count + 1))
    log "$service sustained probe failure $fail_count on port $port but active/recent socks traffic detected (active=$active_count); deferring restart $defer_count/$BUSY_DEFER_THRESHOLD"
    write_state "$sf" "$fail_count" "$defer_count" "$last_failure_ts"
    continue
  fi

  if (( ip_ok == 1 )); then
    log "$service failed OpenAI probe $fail_count times on port $port despite correct egress IP $current_ip"
  else
    log "$service failed functional probe $fail_count times on port $port (openai='${openai_status:-fail}', ip='${current_ip:-fail}', expected='$expected_ip')"
  fi

  restart_tunnel "$service" "$port"

  openai_status=""
  if openai_status="$(probe_openai "$port" 2>/dev/null)" && [[ "$openai_status" == HTTP/* ]]; then
    log "$service recovered after restart on port $port with probe result '$openai_status'"
    clear_state "$sf"
  else
    current_ip="$(probe_ip "$port" 2>/dev/null || true)"
    log "$service still unhealthy after restart on port $port (openai='${openai_status:-fail}', ip='${current_ip:-fail}')"
    write_state "$sf" 0 0 "$(date +%s)"
  fi
done
