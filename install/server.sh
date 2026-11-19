#!/usr/bin/env bash
#
# Vaultix manager one-shot setup.
#
# Installs the manager + postgres stack with Docker Compose:
#   curl -fsSL https://github.com/doko89/vaultix/releases/latest/download/server.sh \
#     | sudo bash -s -- --port 8080
#
# Compose-file mechanism (documented choice): this script downloads
# deploy/docker-compose.yml for the pinned version straight from the tag
# (raw.githubusercontent.com) and adds a small local override that pins the
# manager service to the published ghcr.io image for that same tag. No
# release bundle to keep in sync; the tag is the single source of truth.
# The stack boots WITHOUT a compose profile (postgres + manager only) so no
# dev S3 backend is pulled; object storage is configured post-install via
# the manager's storage API / web UI.
#
# Re-running is safe: existing secrets in $DIR/.env are preserved, `up -d`
# converges the stack, and already-pulled images are reused.
#
set -euo pipefail

REPO="doko89/vaultix"
IMAGE="ghcr.io/doko89/vaultix-manager"
VERSION="latest"
DIR="/opt/vaultix"
PORT=""
PORT_SET="false"
DRY_RUN="false"

usage() {
  cat <<'EOF'
Usage: server.sh [options]

One-shot Vaultix manager setup (Docker Compose: postgres + manager).

Options:
  --version TAG   Release tag to install, or "latest" (default: latest).
                  A bare "1.2.3" is treated as "v1.2.3".
  --dir DIR       Install directory holding docker-compose.yml, the release
                  override and .env (default: /opt/vaultix).
  --port PORT     Host port for the manager UI/API (default: 8080, or the
                  MANAGER_PORT already in DIR/.env on re-runs).
  --dry-run       Print what would be done without changing anything.
  --help          Show this help and exit.

Environment (all optional; secrets are generated when absent):
  POSTGRES_PASSWORD   Postgres password (must be URL-safe: hex recommended).
  VAULTIX_CRYPTO_KEY  Manager 32-byte crypto key (raw 32 chars, base64 or hex).

Examples:
  sudo bash server.sh --port 8080
  sudo POSTGRES_PASSWORD=$(openssl rand -hex 16) bash server.sh --version v1.2.3
EOF
}

log()  { printf '%s\n' "$*"; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:?--version needs a value}"; shift 2 ;;
    --version=*) VERSION="${1#--version=}"; shift ;;
    --dir) DIR="${2:?--dir needs a value}"; shift 2 ;;
    --dir=*) DIR="${1#--dir=}"; shift ;;
    --port) PORT="${2:?--port needs a value}"; PORT_SET="true"; shift 2 ;;
    --port=*) PORT="${1#--port=}"; PORT_SET="true"; shift ;;
    --dry-run) DRY_RUN="true"; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown argument: $1 (see --help)" ;;
  esac
done

[ -n "${VERSION}" ] || die "--version must not be empty"
[ -n "${DIR}" ] || die "--dir must not be empty"

# Normalize a bare "1.2.3" to tag form "v1.2.3".
case "${VERSION}" in
  latest|v*) ;;
  *) VERSION="v${VERSION}" ;;
esac

if [ "${DRY_RUN}" = "true" ]; then
  log "DRY RUN — no changes will be made."
  log "  version:        ${VERSION}$([ "${VERSION}" = "latest" ] && echo " (would resolve via github.com/${REPO}/releases/latest redirect)")"
  log "  dir:            ${DIR}"
  log "  port:           ${PORT:-8080 (or MANAGER_PORT already in .env)}"
  log "  compose file:   https://raw.githubusercontent.com/${REPO}/${VERSION}/deploy/docker-compose.yml"
  log "  manager image:  ${IMAGE}:${VERSION}"
  log "  steps:          check docker + curl, download compose file,"
  log "                  write docker-compose.release.yml + .env (generated secrets"
  log "                  unless POSTGRES_PASSWORD / VAULTIX_CRYPTO_KEY are set),"
  log "                  docker compose up -d, wait for /healthz, print next steps"
  exit 0
fi

# --- prereqs ---------------------------------------------------------------
command -v curl >/dev/null 2>&1 || die "curl is required but not installed"
command -v docker >/dev/null 2>&1 || die "docker is required but not installed (see https://docs.docker.com/engine/install/)"
docker compose version >/dev/null 2>&1 || die "'docker compose' (v2 plugin) is required but not available"
docker info >/dev/null 2>&1 || die "cannot talk to the docker daemon (run as root or add your user to the docker group)"
mkdir -p "${DIR}" || die "cannot create ${DIR} (run as root or pick --dir you own)"

# --- resolve version -------------------------------------------------------
TAG="${VERSION}"
if [ "${VERSION}" = "latest" ]; then
  log "resolving latest release..."
  REDIRECT_URL="$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")" \
    || die "could not resolve the latest release (network or no releases yet)"
  TAG="${REDIRECT_URL##*/}"
  [ -n "${TAG}" ] && [ "${TAG}" != "releases" ] || die "could not parse latest tag from ${REDIRECT_URL}"
  log "latest is ${TAG}"
fi

# --- download compose file for the tag -------------------------------------
log "downloading compose file for ${TAG}..."
curl -fsSL -o "${DIR}/docker-compose.yml" \
  "https://raw.githubusercontent.com/${REPO}/${TAG}/deploy/docker-compose.yml" \
  || die "could not download the compose file for ${TAG}"
grep -q "Vaultix local stacks" "${DIR}/docker-compose.yml" \
  || die "downloaded compose file failed sanity check (unexpected content)"

# --- release override: published image instead of a local build -------------
# The shipped compose file builds the manager from a repo checkout; releases
# ship a prebuilt image, so pin the service to it (same tag).
cat > "${DIR}/docker-compose.release.yml" <<EOF
# Generated by Vaultix server.sh for ${TAG}. Do not edit by hand;
# re-run the installer to regenerate.
services:
  manager:
    image: ${IMAGE}:${TAG}
EOF
log "wrote ${DIR}/docker-compose.release.yml"

# --- .env (preserve existing secrets on re-run) -----------------------------
gen_hex() {  # $1 = bytes; openssl preferred, /dev/urandom fallback
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "$1"
  else
    od -A n -t x1 -N "$1" /dev/urandom | tr -d ' \n'
  fi
}
gen_b64() {  # $1 = bytes of base64-encoded 32-byte crypto key
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 "$1" | tr -d '\n'
  else
    od -A n -t u1 -N "$1" /dev/urandom | python3 -c 'import base64,sys; print(base64.b64encode(bytes(int(x) for x in sys.stdin.read().split())).decode())'
  fi
}

dotenv_get() {  # $1 = key; prints value or empty
  [ -f "${DIR}/.env" ] || return 0
  grep -E "^$1=" "${DIR}/.env" | tail -n 1 | cut -d= -f2- || true
}

EXISTING_PG="$(dotenv_get POSTGRES_PASSWORD)"
EXISTING_CRYPTO="$(dotenv_get VAULTIX_CRYPTO_KEY)"
EXISTING_PORT="$(dotenv_get MANAGER_PORT)"

PG_PASSWORD="${POSTGRES_PASSWORD:-${EXISTING_PG:-}}"
CRYPTO_KEY="${VAULTIX_CRYPTO_KEY:-${EXISTING_CRYPTO:-}}"
[ -n "${PG_PASSWORD}" ] || { PG_PASSWORD="$(gen_hex 16)"; log "generated POSTGRES_PASSWORD"; }
[ -n "${CRYPTO_KEY}" ] || { CRYPTO_KEY="$(gen_b64 32)"; log "generated VAULTIX_CRYPTO_KEY"; }

if [ "${PORT_SET}" = "true" ]; then
  EFFECTIVE_PORT="${PORT}"
elif [ -n "${EXISTING_PORT}" ]; then
  EFFECTIVE_PORT="${EXISTING_PORT}"
else
  EFFECTIVE_PORT="${PORT:-8080}"
fi
case "${EFFECTIVE_PORT}" in
  ''|*[!0-9]*) die "invalid port: ${EFFECTIVE_PORT}" ;;
esac

# Write atomically; secrets never touch stdout.
TMP_ENV="$(mktemp "${DIR}/.env.XXXXXX")"
{
  echo "# Generated by Vaultix server.sh for ${TAG}. Secrets below are"
  echo "# sensitive — keep this file root-only (chmod 600) and backed up."
  echo "POSTGRES_PASSWORD=${PG_PASSWORD}"
  echo "VAULTIX_CRYPTO_KEY=${CRYPTO_KEY}"
  echo "MANAGER_PORT=${EFFECTIVE_PORT}"
} > "${TMP_ENV}"
chmod 600 "${TMP_ENV}"
mv "${TMP_ENV}" "${DIR}/.env"
log "wrote ${DIR}/.env (mode 600)"

# --- boot ------------------------------------------------------------------
COMPOSE="docker compose -f ${DIR}/docker-compose.yml -f ${DIR}/docker-compose.release.yml"
# shellcheck disable=SC2086
${COMPOSE} pull --quiet manager postgres 2>/dev/null || true  # best effort; up pulls anyway
log "starting postgres + manager..."
# shellcheck disable=SC2086
${COMPOSE} up -d

# --- wait healthy ------------------------------------------------------------
log "waiting for the manager to turn healthy (up to 180s)..."
READY="false"
for _ in $(seq 1 90); do
  if curl -fsSL -o /dev/null "http://localhost:${EFFECTIVE_PORT}/healthz" 2>/dev/null; then
    READY="true"
    break
  fi
  sleep 2
done
if [ "${READY}" != "true" ]; then
  die "manager did not turn healthy — inspect with: ${COMPOSE} logs --tail=100 manager"
fi
log "manager is healthy."

# --- next steps --------------------------------------------------------------
cat <<EOF

======================================================================
Vaultix manager ${TAG} is up.
  URL:      http://localhost:${EFFECTIVE_PORT}  (UI + API)
  health:   http://localhost:${EFFECTIVE_PORT}/healthz
  dir:      ${DIR}

WARNING: plain HTTP on localhost WITHOUT TLS. This installer is for local /
lab use. Before exposing the manager: terminate TLS in a reverse proxy,
stop publishing postgres to the host, and read the prod-hardening checklist
at the top of ${DIR}/docker-compose.yml.
======================================================================

1. Seed the first administrator (refuses when users already exist):
     ${COMPOSE} exec \\
       -e VAULTIX_ADMIN_USER=admin \\
       -e VAULTIX_ADMIN_PASSWORD='<choose-a-strong-password>' \\
       manager /usr/local/bin/manager seed-admin

2. Log in at the URL above, then issue an enroll token for each worker VM
   (log in per README "E2E bootstrap" step 4 to get \$S, then):
     curl -s -X POST http://localhost:${EFFECTIVE_PORT}/api/agents/enroll-tokens \\
       -H "Cookie: vaultix_session=\$S" \\
       -H 'X-Requested-With: XMLHttpRequest' \\
       -H 'Content-Type: application/json' -d '{}'

3. On each worker VM, install the agent (see README "Quick setup"):
     curl -fsSL https://github.com/${REPO}/releases/latest/download/worker.sh \\
       | sudo MANAGER_URL=http://<this-host>:${EFFECTIVE_PORT} \\
              VAULTIX_ENROLL_TOKEN=<token-from-step-2> bash -s --

4. Configure object storage post-install (manager boots without an S3
   backend by design): Settings > Storage in the UI, or the storage API.

Stack commands (compose files + secrets live in ${DIR}):
  logs:    ${COMPOSE} logs -f --tail=200
  restart: ${COMPOSE} up -d
  stop:    ${COMPOSE} down        (volumes survive; add -v to wipe)
EOF
