#!/bin/sh
# Coda installer for Linux (Debian/Ubuntu, Fedora/RHEL/Rocky/Alma, openSUSE,
# Arch, Alpine, Void, ...) on amd64 and arm64 hosts (e.g. a Proxmox LXC or VM).
# POSIX sh on purpose: runs on dash, busybox ash and bash with zero
# bootstrap dependencies (Alpine has no bash by default).
#
# One-liner:
#   curl -fsSL https://raw.githubusercontent.com/PPabloMunoz/coda/main/scripts/install.sh | sudo sh
# With options (note the `-s --` separator when piping):
#   curl -fsSL .../scripts/install.sh | sudo sh -s -- --roots /mnt/music --port 8687
#   sudo ./scripts/install.sh --version 0.1.2 --roots /mnt/music:/mnt/other
#
# What it does: downloads the release binary + checksums.txt, verifies the
# checksum, installs to /usr/local/bin/coda, creates a service user, writes a
# service definition (systemd unit, or OpenRC script + conf.d; env-only
# config, no YAML editing), and starts the service. On hosts with neither
# init system it installs the binary and prints how to run it by hand.
# Re-running upgrades the binary in place and restarts the service.
# No pipefail: not POSIX. Pipes below either use fetch helpers (fatal via
# set -e) or treat grep's exit status as the signal, so nothing is masked.
set -eu

REPO="PPabloMunoz/coda"
BIN_NAME="coda"
INSTALL_PATH="/usr/local/bin/coda"

VERSION="latest"
PORT="8687"
ROOTS="/mnt/music"
DATA_DIR="/var/lib/coda"
SERVICE_USER="coda"
WITH_SERVICE=1

usage() {
  cat <<'EOF'
Usage: install.sh [options]

Options:
  --version X.Y.Z   release to install (default: latest stable)
  --port PORT       port coda listens on (default: 8687)
  --roots PATHS     music dirs, ':'-separated like CODA_ROOTS (default: /mnt/music)
  --data-dir DIR    state dir for DB, covers and config.yaml (default: /var/lib/coda)
  --user USER       service user to create/run as (default: coda)
  --no-service      install the binary only, skip service setup
  --help            print this and exit
EOF
}

log() {
  printf '%s\n' "$*"
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

have() {
  command -v "$1" >/dev/null 2>&1
}

while [ $# -gt 0 ]; do
  case "$1" in
  --version)
    VERSION="${2:?--version needs a value}"
    shift 2
    ;;
  --port)
    PORT="${2:?--port needs a value}"
    shift 2
    ;;
  --roots)
    ROOTS="${2:?--roots needs a value}"
    shift 2
    ;;
  --data-dir)
    DATA_DIR="${2:?--data-dir needs a value}"
    shift 2
    ;;
  --user)
    SERVICE_USER="${2:?--user needs a value}"
    shift 2
    ;;
  --no-service)
    WITH_SERVICE=0
    shift
    ;;
  --help)
    usage
    exit 0
    ;;
  --)
    shift
    break
    ;;
  -*) fail "unknown flag: $1 (see --help)" ;;
  *) fail "unexpected argument: $1 (see --help)" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || fail "run as root (e.g. pipe into 'sudo sh')"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
[ "$OS" = "linux" ] || fail "this script targets Linux; on macOS grab the darwin binary or use Docker"
case "$(uname -m)" in
x86_64 | amd64) ARCH="amd64" ;;
aarch64 | arm64) ARCH="arm64" ;;
*) fail "unsupported architecture: $(uname -m) (releases exist for amd64 and arm64)" ;;
esac

# INIT is systemd, openrc, or none. /run/systemd/system is the canonical
# "systemd is PID 1" test (a bare `command -v systemctl` also matches
# containers and chroots where systemd is installed but not running).
if [ -d /run/systemd/system ]; then
  INIT="systemd"
elif [ -d /run/openrc ] || have rc-service; then
  INIT="openrc"
else
  INIT="none"
  WITH_SERVICE=0
fi

# Install a downloader if the host has neither curl nor wget.
install_pkgs() {
  if have apk; then
    apk add --no-cache "$@"
  elif have apt-get; then
    apt-get update -qq
    apt-get install -y -qq "$@"
  elif have dnf; then
    dnf install -y "$@"
  elif have yum; then
    yum install -y "$@"
  elif have zypper; then
    zypper --non-interactive install "$@"
  elif have pacman; then
    pacman -S --noconfirm --needed "$@"
  elif have xbps-install; then
    xbps-install -Sy "$@"
  else
    return 1
  fi
}

if ! have curl && ! have wget; then
  log "installing curl + ca-certificates..."
  install_pkgs curl ca-certificates || fail "curl or wget is required but could not be installed"
fi

if have curl; then
  fetch() { curl -fsSL "$1" -o "$2"; }
  fetch_stdout() { curl -fsSL "$1"; }
else
  fetch() { wget -q -O "$2" "$1"; }
  fetch_stdout() { wget -q -O - "$1"; }
fi

verify_sha256() {
  if have sha256sum; then
    (cd "$TMP" && sha256sum -c expected.sha256)
  elif have shasum; then
    (cd "$TMP" && shasum -a 256 -c expected.sha256)
  else
    fail "sha256sum or shasum is required to verify the download"
  fi
}

if [ "$VERSION" = "latest" ]; then
  TAG="$(fetch_stdout "https://api.github.com/repos/${REPO}/releases/latest" | grep -o '"tag_name": *"[^"]*"' | cut -d'"' -f4)"
  [ -n "$TAG" ] || fail "could not resolve the latest release (GitHub API rate limit? try --version X.Y.Z)"
else
  case "$VERSION" in
  v*) TAG="$VERSION" ;;
  *) TAG="v$VERSION" ;;
  esac
fi
ASSET="${BIN_NAME}-${OS}-${ARCH}"
log "installing ${BIN_NAME} ${TAG} (${OS}/${ARCH})..."

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fetch "https://github.com/${REPO}/releases/download/${TAG}/${ASSET}" "${TMP}/${ASSET}"
fetch "https://github.com/${REPO}/releases/download/${TAG}/checksums.txt" "${TMP}/checksums.txt"
# Exact filename match (checksums.txt lines are "<hash>  <name>" or "<hash> *<name>").
awk -v a="$ASSET" '$2 == a || $2 == "*" a { print; found = 1 } END { exit !found }' \
  "${TMP}/checksums.txt" >"${TMP}/expected.sha256" || fail "no checksum entry for ${ASSET}"
verify_sha256
install -m 0755 "${TMP}/${ASSET}" "$INSTALL_PATH"

if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
  log "creating user ${SERVICE_USER}..."
  NOLOGIN="$(command -v nologin 2>/dev/null || true)"
  [ -n "$NOLOGIN" ] || NOLOGIN="/bin/false"
  if have useradd; then
    # Create the group explicitly: not every distro's useradd does it for --system.
    getent group "$SERVICE_USER" >/dev/null 2>&1 || groupadd --system "$SERVICE_USER"
    useradd --system --no-create-home --shell "$NOLOGIN" -g "$SERVICE_USER" "$SERVICE_USER"
  elif have adduser; then
    # Alpine / busybox.
    getent group "$SERVICE_USER" >/dev/null 2>&1 || addgroup -S "$SERVICE_USER"
    adduser -S -D -H -G "$SERVICE_USER" -s "$NOLOGIN" "$SERVICE_USER"
  else
    fail "cannot create user ${SERVICE_USER}: neither useradd nor adduser found"
  fi
fi
# --user may name an existing account whose primary group differs from its name.
SERVICE_GROUP="$(id -gn "$SERVICE_USER")"

# Optional access probe as the service user. GNU and busybox su both accept
# `-s SHELL USER -c CMD`; if su is missing or refuses, skip the probe.
CAN_SU=0
if have su && su -s /bin/sh "$SERVICE_USER" -c true >/dev/null 2>&1; then
  CAN_SU=1
fi

mkdir -p "$DATA_DIR"
# Split ROOTS on ':' (same separator as CODA_ROOTS on Linux) without arrays.
case "$ROOTS" in
"" | :* | *: | *::*) fail "empty entry in --roots (check for '::' or leading/trailing ':')" ;;
esac
roots_left="$ROOTS"
while [ -n "$roots_left" ]; do
  case "$roots_left" in
  *:*)
    r="${roots_left%%:*}"
    roots_left="${roots_left#*:}"
    ;;
  *)
    r="$roots_left"
    roots_left=""
    ;;
  esac
  [ -n "$r" ] || fail "empty entry in --roots (check for '::' or trailing ':')"
  case "$r" in
  /*) ;;
  *) fail "root must be absolute: $r" ;;
  esac
  mkdir -p "$r"
  chown "$SERVICE_USER:$SERVICE_GROUP" "$r"
  if [ "$CAN_SU" -eq 1 ] && ! su -s /bin/sh "$SERVICE_USER" -c "[ -r '$r' ] && [ -w '$r' ] && [ -x '$r' ]"; then
    log "warning: ${SERVICE_USER} cannot read/write ${r}; linking (which moves files) will fail."
    log "warning: fix with e.g. 'chown -R ${SERVICE_USER}:${SERVICE_GROUP} ${r}' or pass --user <owner>."
  fi
done
chown -R "$SERVICE_USER:$SERVICE_GROUP" "$DATA_DIR"

# SELinux (Fedora/RHEL family): make sure the new paths carry default labels.
if have restorecon; then
  restorecon -R "$INSTALL_PATH" "$DATA_DIR" >/dev/null 2>&1 || true
fi

if [ "$WITH_SERVICE" -eq 0 ]; then
  if [ "$INIT" = "none" ]; then
    log "no supported init system found (need systemd or OpenRC); skipping service setup."
  else
    log "skipping service setup (--no-service)."
  fi
  log "run manually: CODA_PORT=${PORT} CODA_ROOTS=${ROOTS} CODA_CONFIG=${DATA_DIR}/config.yaml CODA_DB_PATH=${DATA_DIR}/coda.db ${INSTALL_PATH}"
  exit 0
fi

if [ "$INIT" = "openrc" ]; then
  log "writing OpenRC service..."
  mkdir -p /etc/conf.d
  # `export` so the daemon inherits these; plain assignments in conf.d are
  # only visible to the init script itself.
  cat >"/etc/conf.d/${BIN_NAME}" <<EOF
export CODA_PORT="${PORT}"
export CODA_ROOTS="${ROOTS}"
export CODA_CONFIG="${DATA_DIR}/config.yaml"
export CODA_DB_PATH="${DATA_DIR}/coda.db"
export GIN_MODE="release"
EOF
  cat >"/etc/init.d/${BIN_NAME}" <<EOF
#!/sbin/openrc-run
name="${BIN_NAME}"
description="Coda music library manager"
command="${INSTALL_PATH}"
command_user="${SERVICE_USER}:${SERVICE_GROUP}"
command_background="yes"
pidfile="/run/${BIN_NAME}.pid"
output_log="/var/log/${BIN_NAME}.log"
error_log="/var/log/${BIN_NAME}.log"

depend() {
	need net
	after firewall
}
EOF
  chmod +x "/etc/init.d/${BIN_NAME}"
  touch "/var/log/${BIN_NAME}.log"
  chown "$SERVICE_USER:$SERVICE_GROUP" "/var/log/${BIN_NAME}.log"
  rc-update add "$BIN_NAME" default
  rc-service "$BIN_NAME" stop >/dev/null 2>&1 || true
  rc-service "$BIN_NAME" start
  LOG_HINT="tail -f /var/log/${BIN_NAME}.log"
  FAIL_HINT="see '/var/log/${BIN_NAME}.log'"
else
  log "writing systemd unit..."
  cat >"/etc/systemd/system/${BIN_NAME}.service" <<EOF
[Unit]
Description=Coda music library manager
After=network-online.target
Wants=network-online.target

[Service]
User=${SERVICE_USER}
Group=${SERVICE_GROUP}
Environment="CODA_PORT=${PORT}"
Environment="CODA_ROOTS=${ROOTS}"
Environment="CODA_CONFIG=${DATA_DIR}/config.yaml"
Environment="CODA_DB_PATH=${DATA_DIR}/coda.db"
Environment="GIN_MODE=release"
ExecStart=${INSTALL_PATH}
Restart=on-failure
RestartSec=5
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable "$BIN_NAME"
  # restart (not just start) so re-running the script picks up the new binary.
  systemctl restart "$BIN_NAME"
  LOG_HINT="journalctl -u ${BIN_NAME} -f"
  FAIL_HINT="see 'journalctl -u ${BIN_NAME} -e'"
fi

log "waiting for health check..."
ok=0
tries=0
while [ "$tries" -lt 30 ]; do
  if fetch_stdout "http://127.0.0.1:${PORT}/api/v1/health" 2>/dev/null | grep -q ok; then
    ok=1
    break
  fi
  tries=$((tries + 1))
  sleep 1
done
[ "$ok" -eq 1 ] || fail "service did not become healthy; ${FAIL_HINT}"

# `hostname -I` is a GNU/net-tools extension (absent on busybox); fall back to
# the source address of the default route, then to localhost.
IP="$(hostname -I 2>/dev/null | awk '{print $1}')" || IP=""
if [ -z "$IP" ] && have ip; then
  IP="$(ip -4 -o route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit }}')" || IP=""
fi
log ""
log "${BIN_NAME} ${TAG} is up: http://${IP:-localhost}:${PORT}"
log "config: ${DATA_DIR}/config.yaml   db: ${DATA_DIR}/coda.db"
log "logs:   ${LOG_HINT}"
log "upgrade: re-run this script (optionally with --version X.Y.Z)"
