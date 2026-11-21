#!/usr/bin/env bash
#
# Vaultix agent (worker) one-shot setup for macOS.
#
# Installs the agent binary, enrolls this Mac with the manager, and starts
# the launchd daemon:
#   curl -fsSL https://github.com/doko89/vaultix/releases/latest/download/worker-macos.sh \
#     | sudo MANAGER_URL=https://manager:8443 VAULTIX_ENROLL_TOKEN=<token> bash -s --
#
# The --token / VAULTIX_ENROLL_TOKEN value is the SINGLE-USE enroll token
# issued by the manager (POST /api/agents/enroll-tokens). This script redeems
# it (POST /api/agent/enroll with this host's name) and persists the
# resulting agent credentials via `vaultix-agent enroll` — the enroll token
# itself is never stored.
#
# Re-running with a FRESH enroll token re-enrolls this Mac (config + binary
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
Usage: worker-macos.sh [options]

One-shot Vaultix agent setup for macOS: downloads the agent binary,
redeems the enroll token, writes config + launchd daemon, starts the
service. Must run as root (sudo).

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
  sudo MANAGER_URL=https://manager:8443 VAULTIX_ENROLL_TOKEN=<token> bash worker-macos.sh
  sudo bash worker-macos.sh --manager https://manager:8443 --token <token> --version v1.2.3
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

# Architecture detection (releases ship darwin/amd64 + darwin/arm64).
ARCH=""
case "$(uname -m)" in
  x86_64) ARCH="amd64" ;;
  arm64) ARCH="arm64" ;;
  *) die "unsupported architecture: $(uname -m) (releases ship amd64 and arm64 only)" ;;
esac

EFFECTIVE_HOST="${HOSTNAME_OVERRIDE:-$(hostname)}"

if [ "${DRY_RUN}" = "true" ]; then
  log "DRY RUN — no changes will be made."
  log "  manager:   ${MANAGER:-<missing: pass --manager or MANAGER_URL>}"
  log "  token:     $([ -n "${TOKEN}" ] && echo "<provided>" || echo "<missing: pass --token or VAULTIX_ENROLL_TOKEN>")"
  log "  hostname:  ${EFFECTIVE_HOST}"
  log "  version:   ${VERSION}$([ "${VERSION}" = "latest" ] && echo " (would resolve via github.com/${REPO}/releases/latest redirect)")"
  log "  asset:     https://github.com/${REPO}/releases/download/${VERSION}/vaultix-agent-darwin-${ARCH}"
  log "  steps:     check root + curl/python3/launchctl, download + sha256-verify"
  log "             agent binary to /usr/local/bin/vaultix-agent, redeem enroll"
  log "             token at POST /api/agent/enroll, persist credentials in"
  log "             /etc/vaultix/agent.yaml, write launchd daemon,"
  log "             launchctl bootstrap + kickstart com.vaultix.agent"
  [ -n "${MANAGER}" ] || die "dry-run: --manager / MANAGER_URL is required"
  [ -n "${TOKEN}" ] || die "dry-run: --token / VAULTIX_ENROLL_TOKEN is required"
  exit 0
fi

# --- required inputs ---------------------------------------------------------
[ -n "${MANAGER}" ] || die "--manager / MANAGER_URL is required"
[ -n "${TOKEN}" ] || die "--token / VAULTIX_ENROLL_TOKEN is required"
[ "$(uname -s)" = "Darwin" ] || die "this installer targets macOS only"
[ "$(id -u)" = "0" ] || die "must run as root (sudo; writes /usr/local/bin, /etc/vaultix, launchd daemons)"

# --- prereqs -----------------------------------------------------------------
command -v curl >/dev/null 2>&1 || die "curl is required but not installed"
command -v python3 >/dev/null 2>&1 || die "python3 is required but not installed (parses the enroll response; install Command Line Tools: xcode-select --install)"
python3 -c 'pass' 2>/dev/null || die "python3 is a stub (Command Line Tools missing) — run: xcode-select --install"
command -v launchctl >/dev/null 2>&1 || die "launchctl is required but not installed"
command -v shasum >/dev/null 2>&1 || die "shasum is required but not installed (macOS ships it with perl)"

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
ASSET="vaultix-agent-darwin-${ARCH}"
TMPDIR_WORK="$(mktemp -d)"
trap 'rm -rf "${TMPDIR_WORK}"' EXIT
log "downloading ${ASSET} (${TAG})..."
curl -fsSL -o "${TMPDIR_WORK}/${ASSET}" \
  "https://github.com/${REPO}/releases/download/${TAG}/${ASSET}" \
  || die "could not download ${ASSET} for ${TAG}"
curl -fsSL -o "${TMPDIR_WORK}/sha256sums.txt" \
  "https://github.com/${REPO}/releases/download/${TAG}/sha256sums.txt" \
  || die "could not download sha256sums.txt for ${TAG}"
(cd "${TMPDIR_WORK}" && grep -F " ${ASSET}" sha256sums.txt | shasum -a 256 -c -) \
  || die "checksum verification FAILED for ${ASSET} (refusing to install)"
install -m 0755 "${TMPDIR_WORK}/${ASSET}" /usr/local/bin/vaultix-agent
log "installed /usr/local/bin/vaultix-agent ($(vaultix-agent version 2>/dev/null || echo 'version check skipped'))"
# Clear quarantine so Gatekeeper does not block the fresh binary.
xattr -d com.apple.quarantine /usr/local/bin/vaultix-agent 2>/dev/null || true

# --- redeem the enroll token ---------------------------------------------------
# POST /api/agent/enroll {"token","hostname","os","architecture"} -> 201 {"agent_id","token",...}.
# The enroll token is single-use; the RESPONSE credential is what the agent
# persists. Neither secret is ever printed.
log "redeeming enroll token at ${MANAGER} (hostname: ${EFFECTIVE_HOST}, darwin/${ARCH})..."
PAYLOAD="$(MANAGER_TOKEN="${TOKEN}" ENROLL_HOST="${EFFECTIVE_HOST}" ENROLL_ARCH="${ARCH}" python3 -c \
  'import json,os; print(json.dumps({"token": os.environ["MANAGER_TOKEN"], "hostname": os.environ["ENROLL_HOST"], "os": "darwin", "architecture": os.environ["ENROLL_ARCH"]}))')"
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
mkdir -p /etc/vaultix /var/lib/vaultix/work /var/log/vaultix
VAULTIX_AGENT_STATE_FILE=/var/lib/vaultix/agent-state.json \
VAULTIX_AGENT_WORK_DIR=/var/lib/vaultix/work \
  vaultix-agent enroll --config /etc/vaultix/agent.yaml \
    --manager "${MANAGER}" --token "${AGENT_CRED}" --agent-id "${AGENT_ID}"
chmod 600 /etc/vaultix/agent.yaml
log "wrote /etc/vaultix/agent.yaml (mode 600)"

# --- launchd daemon ------------------------------------------------------------
cat > /Library/LaunchDaemons/com.vaultix.agent.plist <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.vaultix.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/vaultix-agent</string>
    <string>run</string>
    <string>--config</string>
    <string>/etc/vaultix/agent.yaml</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>StandardOutPath</key>
  <string>/var/log/vaultix/agent.log</string>
  <key>StandardErrorPath</key>
  <string>/var/log/vaultix/agent.log</string>
  <key>WorkingDirectory</key>
  <string>/var/lib/vaultix</string>
</dict>
</plist>
EOF
# launchd refuses daemons with insecure ownership: root-owned, not writable
# by group/other.
chown root:wheel /Library/LaunchDaemons/com.vaultix.agent.plist
chmod 644 /Library/LaunchDaemons/com.vaultix.agent.plist
log "wrote /Library/LaunchDaemons/com.vaultix.agent.plist"

# Unload a previous copy (re-runs), then load fresh. bootstrap/kickstart
# is the modern path; load -w covers older macOS.
launchctl bootout system/com.vaultix.agent 2>/dev/null || true
if launchctl bootstrap system /Library/LaunchDaemons/com.vaultix.agent.plist 2>/dev/null; then
  launchctl enable system/com.vaultix.agent 2>/dev/null || true
  launchctl kickstart -k system/com.vaultix.agent
else
  launchctl load -w /Library/LaunchDaemons/com.vaultix.agent.plist \
    || die "could not load the launchd daemon"
fi
sleep 3
launchctl print system/com.vaultix.agent >/dev/null 2>&1 \
  || die "com.vaultix.agent is not running — inspect with: tail -100 /var/log/vaultix/agent.log"

cat <<EOF

======================================================================
Vaultix agent ${TAG} enrolled as ${AGENT_ID} and running.
  manager:  ${MANAGER}
  hostname: ${EFFECTIVE_HOST}
  config:   /etc/vaultix/agent.yaml
======================================================================

Follow the agent log with:
  tail -f /var/log/vaultix/agent.log
EOF
