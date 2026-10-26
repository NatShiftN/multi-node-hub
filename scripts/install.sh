#!/usr/bin/env bash
# NodeHub agent bootstrap. Intended to be run as root exactly once by the
# control plane's SSH provisioning path.
set -Eeuo pipefail

trap 'printf "nodehub-install: failed at line %s\n" "$LINENO" >&2' ERR
umask 077

TOKEN=""
SERVER=""
NODE_ID=""
AGENT_URL=""

die() {
  printf 'nodehub-install: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: install.sh --token TOKEN --server WS_URL --id NODE_ID [--agent-url URL]

Installs /usr/local/bin/nodehub-agent and a systemd service, then starts it.
The token and password are never printed by this script.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --token)
      [ "$#" -ge 2 ] || die "--token requires a value"
      TOKEN="$2"
      shift 2
      ;;
    --server)
      [ "$#" -ge 2 ] || die "--server requires a value"
      SERVER="$2"
      shift 2
      ;;
    --id)
      [ "$#" -ge 2 ] || die "--id requires a value"
      NODE_ID="$2"
      shift 2
      ;;
    --agent-url)
      [ "$#" -ge 2 ] || die "--agent-url requires a value"
      AGENT_URL="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

[ "$(id -u)" -eq 0 ] || die "must run as root"
[ -n "$TOKEN" ] || die "--token is required"
[ -n "$SERVER" ] || die "--server is required"
[ -n "$NODE_ID" ] || die "--id is required"
command -v systemctl >/dev/null 2>&1 || die "systemd is required"
command -v install >/dev/null 2>&1 || die "install(1) is required"

[[ "$TOKEN" =~ ^[A-Za-z0-9_-]{16,128}$ ]] || die "invalid token format"
[[ "$NODE_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || die "invalid node id"
case "$SERVER" in
  ws://*/ws/agent|wss://*/ws/agent) ;;
  *) die "invalid server URL: $SERVER" ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64|aarch64|arm64) ;;
  *) die "unsupported architecture $ARCH; supported: x86_64/amd64, aarch64/arm64" ;;
esac

if [ -z "$AGENT_URL" ]; then
  case "$SERVER" in
    ws://*/ws/agent)
      AGENT_URL="http://${SERVER#ws://}"
      ;;
    wss://*/ws/agent)
      AGENT_URL="https://${SERVER#wss://}"
      ;;
    *)
      die "--agent-url is required for this server URL"
      ;;
  esac
  AGENT_URL="${AGENT_URL%/ws/agent}/downloads/nodehub-agent-linux-amd64"
fi
case "$ARCH" in
  aarch64|arm64) AGENT_URL="${AGENT_URL/linux-amd64/linux-arm64}" ;;
esac
case "$AGENT_URL" in
  http://*|https://*) ;;
  *) die "invalid agent URL: $AGENT_URL" ;;
esac

TMP_BIN="$(mktemp /tmp/nodehub-agent.XXXXXX)"
TMP_SERVICE="$(mktemp /tmp/nodehub-agent.service.XXXXXX)"
# shellcheck disable=SC2329 # invoked by trap
cleanup() {
  rm -f "$TMP_BIN" "$TMP_SERVICE"
}
trap cleanup EXIT

if command -v curl >/dev/null 2>&1; then
  curl -fsSL --connect-timeout 10 --max-time 120 "$AGENT_URL" -o "$TMP_BIN"
elif command -v wget >/dev/null 2>&1; then
  wget -q --timeout=120 -O "$TMP_BIN" "$AGENT_URL"
else
  die "curl or wget is required"
fi

[ -s "$TMP_BIN" ] || die "downloaded agent is empty"
BYTES="$(wc -c < "$TMP_BIN" | tr -d ' ')"
[ "$BYTES" -gt 1000000 ] || die "downloaded agent is too small; check the control-plane URL"

install -m 0755 "$TMP_BIN" /usr/local/bin/nodehub-agent

# systemd expands % specifiers, and arguments are double-quoted in ExecStart.
# Escape % and rely on strict validation above for all other characters.
SERVICE_SERVER="${SERVER//%/%%}"
SERVICE_TOKEN="${TOKEN//%/%%}"
SERVICE_NODE_ID="${NODE_ID//%/%%}"

cat > "$TMP_SERVICE" <<EOF
[Unit]
Description=NodeHub Edge Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/nodehub-agent --server "${SERVICE_SERVER}" --token "${SERVICE_TOKEN}" --id "${SERVICE_NODE_ID}"
Restart=always
RestartSec=5
LimitNOFILE=65535
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF

install -m 0600 "$TMP_SERVICE" /etc/systemd/system/nodehub-agent.service
systemctl daemon-reload
systemctl enable nodehub-agent.service >/dev/null 2>&1
if ! systemctl restart nodehub-agent.service; then
  journalctl -u nodehub-agent.service -n 30 --no-pager >&2 2>/dev/null || true
  die "nodehub-agent service restart failed"
fi

for _ in 1 2 3 4 5; do
  if systemctl is-active --quiet nodehub-agent.service; then
    printf 'nodehub-agent installed and running (node id: %s)\n' "$NODE_ID"
    exit 0
  fi
  sleep 1
done

journalctl -u nodehub-agent.service -n 30 --no-pager >&2 2>/dev/null || true
printf 'nodehub-agent failed to start; inspect: journalctl -u nodehub-agent.service\n' >&2
exit 1
