#!/usr/bin/env bash
set -euo pipefail

TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-15}"
OPENAI_PROBE_URL="${OPENAI_PROBE_URL:-https://chatgpt.com/backend-api/codex/responses}"
IP_PROBE_URL="${IP_PROBE_URL:-https://api.ipify.org}"

services=(
  "jp-relay-1-tunnel.service|20081|141.11.138.77"
  "jp-relay-2-tunnel.service|20082|141.11.139.81"
  "jp-relay-3-tunnel.service|20083|141.11.138.243"
)

log() {
  logger -t jp-relay-watchdog "$1"
  echo "$1"
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

for entry in "${services[@]}"; do
  IFS='|' read -r service port expected_ip <<<"$entry"

  openai_status=""
  if openai_status="$(probe_openai "$port" 2>/dev/null)" && [[ "$openai_status" == HTTP/* ]]; then
    continue
  fi

  current_ip=""
  if current_ip="$(probe_ip "$port" 2>/dev/null)" && [[ "$current_ip" == "$expected_ip" ]]; then
    log "$service failed OpenAI probe on port $port despite correct egress IP $current_ip; restarting tunnel defensively"
  else
    log "$service failed functional probe on port $port (openai='${openai_status:-fail}', ip='${current_ip:-fail}', expected='${expected_ip}'); restarting tunnel"
  fi

  systemctl restart "$service"
  sleep 3

  openai_status=""
  if openai_status="$(probe_openai "$port" 2>/dev/null)" && [[ "$openai_status" == HTTP/* ]]; then
    log "$service recovered after restart on port $port with probe result '$openai_status'"
  else
    current_ip="$(probe_ip "$port" 2>/dev/null || true)"
    log "$service still unhealthy after restart on port $port (openai='${openai_status:-fail}', ip='${current_ip:-fail}')"
  fi
done
