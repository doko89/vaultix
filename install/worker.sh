#!/usr/bin/env bash
#
# Vaultix agent (worker) one-shot setup.
#
# Installs the agent binary, enrolls this VM with the manager, and starts
# the systemd service:
#   curl -fsSL https://github.com/doko89/vaultix/releases/latest/download/worker.sh \
#     | sudo MANAGER_URL=http://manager:8080 VAULTIX_ENROLL_TOKEN=<token> bash -s --
#
# The --token / VAULTIX_ENROLL_TOKEN value is the SINGLE-USE enroll token
# issued by the manager (POST /api/agents/enroll-tokens, see the manager
# installer's next steps). This script redeems it (POST /api/agent/enroll
# with this host's name) and persists the resulting agent credentials via
# `vaultix-agent enroll` — the enroll token itself is never stored.
#
# Re-running with a FRESH enroll token re-enrolls this VM (config + binary
# replaced); re-running with an already-redeemed token fails loudly at the
# redeem step with the manager's 401.
#
set -euo pipefail

REPO="doko89/vaultix"
VERSION="latest"
MANAGER="${MANAGER_URL:-}"
TOKEN="${VAULTIX_ENROLL_TOKEN:-}"
HOSTNAME_OVERRIDE=""
DRY_RUN="false"

usage() {
  cat <<'EOF'
Usage: worker.sh [options]

One-shot Vaultix agent setup: downloads the agent binary, redeems the
enroll token, writes config + systemd unit, starts the service. Must run
as root.

Options:
  --manager URL   Manager base URL (or env MANAGER_URL). Required.
  --token TOKEN   Single-use enroll token from the manager
                  (or env VAULTIX_ENROLL_TOKEN). Required.
  --hostname NAME Hostname registered at enroll (default: `hostname`).
  --version TAG   Release tag to install, or "latest" (default: latest).
                  A bare "1.2.3" is treated as "v1.2.3".
  --dry-run       Print what would be done without changing anything.
  --help          Show this help and exit.

Examples:
  sudo MANAGER_URL=http://manager:8080 VAULTIX_ENROLL_TOKEN=<token> bash worker.sh
  sudo bash worker.sh --manager http://manager:8080 --token <token> --version v1.2.3
EOF
}

log()  { printf '%s\n' "$*"; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --manager) MANAGER="${2:?--manager needs a value}"; shift 2 ;;
    --manager=*) MANAGER="${1#--manager=}"; shift ;;
    --token) TOKEN="${2:?--token needs a value}"; shift 2 ;;
    --token=*) TOKEN="${1#--token=}"; shift ;;
    --hostname) HOSTNAME_OVERRIDE="${2:?--hostname needs a value}"; shift 2 ;;
    --hostname=*) HOSTNAME_OVERRIDE="${1#--hostname=}"; shift ;;
    --version) VERSION="${2:?--version needs a value}"; shift 2 ;;
    --version=*) VERSION="${1#--version=}"; shift ;;
    --dry-run) DRY_RUN="true"; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown argument: $1 (see --help)" ;;
  esac
done

[ -n "${VERSION}" ] || die "--version must not be empty"

# Normalize a bare "1.2.3" to tag form "v1.2.3".
case "${VERSION}" in
  latest|v*) ;;
  *) VERSION="v${VERSION}" ;;
esac

# Architecture detection (release ships linux/amd64 + linux/arm64 only).
ARCH=""
case "$(uname -m)" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "unsupported architecture: $(uname -m) (releases ship amd64 and arm64 only)" ;;
esac

EFFECTIVE_HOST="${HOSTNAME_OVERRIDE:-$(hostname)}"

if [ "${DRY_RUN}" = "true" ]; then
  log "DRY RUN — no changes will be made."
  log "  manager:   ${MANAGER:-<missing: pass --manager or MANAGER_URL>}"
  log "  token:     $([ -n "${TOKEN}" ] && echo "<provided>" || echo "<missing: pass --token or VAULTIX_ENROLL_TOKEN>")"
  log "  hostname:  ${EFFECTIVE_HOST}"
  log "  version:   ${VERSION}$([ "${VERSION}" = "latest" ] && echo " (would resolve via github.com/${REPO}/releases/latest redirect)")"
  log "  asset:     https://github.com/${REPO}/releases/download/${VERSION}/vaultix-agent-linux-${ARCH}"
  log "  steps:     check root + curl/python3/systemd, download + sha256-verify"
  log "             agent binary to /usr/local/bin/vaultix-agent, create vaultix"
  log "             user, redeem enroll token at POST /api/agent/enroll, persist"
  log "             credentials in /etc/vaultix/agent.yaml, write systemd unit,"
  log "             systemctl enable --now vaultix-agent"
  [ -n "${MANAGER}" ] || die "dry-run: --manager / MANAGER_URL is required"
  [ -n "${TOKEN}" ] || die "dry-run: --token / VAULTIX_ENROLL_TOKEN is required"
  exit 0
fi

# --- required inputs ---------------------------------------------------------
[ -n "${MANAGER}" ] || die "--manager / MANAGER_URL is required"
[ -n "${TOKEN}" ] || die "--token / VAULTIX_ENROLL_TOKEN is required"
[ "$(id -u)" = "0" ] || die "must run as root (writes /usr/local/bin, /etc/vaultix, systemd units)"

# --- prereqs -----------------------------------------------------------------
command -v curl >/dev/null 2>&1 || die "curl is required but not installed"
command -v python3 >/dev/null 2>&1 || die "python3 is required but not installed (parses the enroll response)"
command -v systemctl >/dev/null 2>&1 || die "systemctl is required but not installed (this installer targets systemd hosts)"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required but not installed"
[ -d /etc/systemd/system ] || die "/etc/systemd/system is missing (not a systemd host?)"

# --- resolve version ---------------------------------------------------------
TAG="${VERSION}"
if [ "${VERSION}" = "latest" ]; then
  log "resolving latest release..."
  REDIRECT_URL="$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")" \
    || die "could not resolve the latest release (network or no releases yet)"
  TAG="${REDIRECT_URL##*/}"
  [ -n "${TAG}" ] && [ "${TAG}" != "releases" ] || die "could not parse latest tag from ${REDIRECT_URL}"
  log "latest is ${TAG}"
fi

# --- download + verify agent binary ------------------------------------------
ASSET="vaultix-agent-linux-${ARCH}"
TMPDIR_WORK="$(mktemp -d)"
trap 'rm -rf "${TMPDIR_WORK}"' EXIT
log "downloading ${ASSET} (${TAG})..."
curl -fsSL -o "${TMPDIR_WORK}/${ASSET}" \
  "https://github.com/${REPO}/releases/download/${TAG}/${ASSET}" \
  || die "could not download ${ASSET} for ${TAG}"
curl -fsSL -o "${TMPDIR_WORK}/sha256sums.txt" \
  "https://github.com/${REPO}/releases/download/${TAG}/sha256sums.txt" \
  || die "could not download sha256sums.txt for ${TAG}"
(cd "${TMPDIR_WORK}" && grep -F " ${ASSET}" sha256sums.txt | sha256sum -c -) \
  || die "checksum verification FAILED for ${ASSET} (refusing to install)"
install -m 0755 "${TMPDIR_WORK}/${ASSET}" /usr/local/bin/vaultix-agent
log "installed /usr/local/bin/vaultix-agent ($(vaultix-agent version 2>/dev/null || echo 'version check skipped'))"

# --- service user --------------------------------------------------------------
if ! id vaultix >/dev/null 2>&1; then
  useradd --system --no-create-home --home-dir /var/lib/vaultix \
    --shell /usr/sbin/nologin vaultix \
    || die "could not create the vaultix system user"
  log "created vaultix system user"
fi
mkdir -p /etc/vaultix /var/lib/vaultix/work
chown vaultix:vaultix /var/lib/vaultix /var/lib/vaultix/work

# --- redeem the enroll token ---------------------------------------------------
# POST /api/agent/enroll {"token","hostname"} -> 201 {"agent_id","token",...}.
# The enroll token is single-use; the RESPONSE credential is what the agent
# persists. Neither secret is ever printed.
log "redeeming enroll token at ${MANAGER} (hostname: ${EFFECTIVE_HOST})..."
PAYLOAD="$(MANAGER_TOKEN="${TOKEN}" ENROLL_HOST="${EFFECTIVE_HOST}" python3 -c \
  'import json,os; print(json.dumps({"token": os.environ["MANAGER_TOKEN"], "hostname": os.environ["ENROLL_HOST"]}))')"
BODY_FILE="$(mktemp)"
HTTP_CODE="$(curl -sS -o "${BODY_FILE}" -w '%{http_code}' -X POST "${MANAGER}/api/agent/enroll" \
  -H 'Content-Type: application/json' -d "${PAYLOAD}")" \
  || die "could not reach ${MANAGER}/api/agent/enroll (is the manager up?)"
if [ "${HTTP_CODE}" != "201" ]; then
  rm -f "${BODY_FILE}"
  die "enroll redeem failed (HTTP ${HTTP_CODE}) — the token may be wrong, expired, or already redeemed (tokens are single-use; issue a fresh one)"
fi
REDEEMED="$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d.get("agent_id","")); print(d.get("token",""))' "${BODY_FILE}")"
rm -f "${BODY_FILE}"
AGENT_ID="$(printf '%s' "${REDEEMED}" | sed -n '1p')"
AGENT_CRED="$(printf '%s' "${REDEEMED}" | sed -n '2p')"
[ -n "${AGENT_ID}" ] && [ -n "${AGENT_CRED}" ] || die "enroll response was missing agent_id/token"
log "redeemed: agent ${AGENT_ID}"

# --- persist agent config ------------------------------------------------------
# State/work paths are pinned under /var/lib/vaultix via the agent's
# VAULTIX_AGENT_* env overrides (defaults would point at root's home since
# enrollment runs as root, but the service runs as vaultix).
VAULTIX_AGENT_STATE_FILE=/var/lib/vaultix/agent-state.json \
VAULTIX_AGENT_WORK_DIR=/var/lib/vaultix/work \
  vaultix-agent enroll --config /etc/vaultix/agent.yaml \
    --manager "${MANAGER}" --token "${AGENT_CRED}" --agent-id "${AGENT_ID}"
chown vaultix:vaultix /etc/vaultix/agent.yaml
chmod 600 /etc/vaultix/agent.yaml
log "wrote /etc/vaultix/agent.yaml (mode 600, vaultix-owned)"

# --- systemd unit (based on deploy/agent/vaultix-agent.service) -----------------
cat > /etc/systemd/system/vaultix-agent.service <<'EOF'
[Unit]
Description=Vaultix Agent - per-VM backup executor
Documentation=https://github.com/doko89/vaultix
After=network-online.target docker.service
Wants=network-online.target
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
Type=simple
User=vaultix
Group=vaultix
# NOTE: for Docker discovery the vaultix user must be in the docker group
# (highly privileged; only enable when Docker integration is configured).
# Enrollment secret / overrides; optional file, absent by default.
EnvironmentFile=-/etc/vaultix/agent.env
# Agent config file (viper): manager URL, TLS, log level, data dir.
ExecStart=/usr/local/bin/vaultix-agent run --config /etc/vaultix/agent.yaml
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=10
TimeoutStopSec=60
# Local job state for restart recovery (TECH_SPEC section 4).
WorkingDirectory=/var/lib/vaultix
LogsDirectory=vaultix
StateDirectory=vaultix
StateDirectoryMode=0750
# Hardening: agent reads backup sources (read-only mounts stay readable) and
# shells out to db tools; confine writes to its state/log dirs.
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/vaultix /var/log/vaultix
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK
RestrictNamespaces=true
LockPersonality=true
SystemCallArchitectures=native
# MemoryDenyWriteExecute intentionally off: Go runtime needs executable heap.
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
log "wrote /etc/systemd/system/vaultix-agent.service"

systemctl daemon-reload
systemctl enable --now vaultix-agent
sleep 3
systemctl is-active --quiet vaultix-agent \
  || die "vaultix-agent is not running — inspect with: journalctl -u vaultix-agent -n 100 --no-pager"

cat <<EOF

======================================================================
Vaultix agent ${TAG} enrolled as ${AGENT_ID} and running.
  manager:  ${MANAGER}
  hostname: ${EFFECTIVE_HOST}
  config:   /etc/vaultix/agent.yaml
======================================================================

EOF
systemctl status vaultix-agent --no-pager || true
cat <<'EOF'

Follow the agent log with:
  journalctl -u vaultix-agent -f
EOF
