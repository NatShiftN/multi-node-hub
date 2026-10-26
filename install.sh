#!/usr/bin/env sh
set -eu

# NodeHub one-command Docker install.
# Usage: ./install.sh
# Optional: NODEHUB_PUBLIC_URL=https://panel.example.com ./install.sh

cd "$(dirname "$0")"

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required: https://docs.docker.com/engine/install/" >&2
  exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "Docker Compose v2 is required (docker compose)." >&2
  exit 1
fi

random() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "$1"
  else
    head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

if [ ! -f .env ]; then
  admin_password="NodeHub-$(random 6)"
  cat > .env <<EOF
NODEHUB_PUBLIC_URL=${NODEHUB_PUBLIC_URL:-http://localhost:8080}
AGENT_TOKEN=$(random 24)
ADMIN_USERNAME=admin
ADMIN_PASSWORD=${admin_password}
JWT_SECRET=$(random 32)
EOF
  echo "Created .env with random credentials."
else
  admin_password="$(grep '^ADMIN_PASSWORD=' .env | cut -d= -f2- || true)"
fi

docker compose up -d --build

echo
echo "NodeHub is starting."
echo "URL:    ${NODEHUB_PUBLIC_URL:-http://localhost:8080}"
echo "User:   admin"
if [ -n "${admin_password:-}" ]; then
  echo "Pass:   ${admin_password}"
else
  echo "Pass:   see ADMIN_PASSWORD in .env"
fi
echo
echo "Next: open the URL, add a node, and copy its install command."
