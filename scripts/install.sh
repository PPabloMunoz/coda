#!/bin/sh
# Coda installer for Debian/Ubuntu and Alpine hosts (e.g. a Proxmox LXC or VM).
# POSIX sh on purpose: runs on dash, busybox ash and bash with zero
# bootstrap dependencies (Alpine has no bash by default).
#
# One-liner:
#   curl -fsSL https://raw.githubusercontent.com/PPabloMunoz/coda/main/scripts/install.sh | sudo bash
# With options (note the `-s --` separator when piping):
#   curl -fsSL .../scripts/install.sh | sudo bash -s -- --roots /mnt/music --port 8687
#   sudo ./scripts/install.sh --version 0.1.2 --roots /mnt/music:/mnt/other
#
# What it does: downloads the release binary + checksums.txt, verifies the
# checksum, installs to /usr/local/bin/coda, creates a service user, writes a
# service definition (systemd unit on Debian/Ubuntu, OpenRC script + conf.d on
# Alpine; env-only config, no YAML editing), and starts the service.
# Re-running upgrades the binary in place and restarts the service.
# No pipefail: not POSIX. Pipes below either use curl -fsSL (fatal via
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

while [ $# -gt 0 ]; do
	case "$1" in
		--version) VERSION="${2:?--version needs a value}"; shift 2 ;;
		--port) PORT="${2:?--port needs a value}"; shift 2 ;;
		--roots) ROOTS="${2:?--roots needs a value}"; shift 2 ;;
		--data-dir) DATA_DIR="${2:?--data-dir needs a value}"; shift 2 ;;
		--user) SERVICE_USER="${2:?--user needs a value}"; shift 2 ;;
		--no-service) WITH_SERVICE=0; shift ;;
		--help) usage; exit 0 ;;
		--) shift; break ;;
		-*) fail "unknown flag: $1 (see --help)" ;;
		*) fail "unexpected argument: $1 (see --help)" ;;
	esac
done

[ "$(id -u)" -eq 0 ] || fail "run as root (e.g. pipe into 'sudo bash')"

# INIT is systemd (Debian/Ubuntu), openrc (Alpine), or none.
if [ -d /run/openrc ] || command -v rc-service >/dev/null 2>&1; then
	INIT="openrc"
elif command -v systemctl >/dev/null 2>&1; then
	INIT="systemd"
else
	INIT="none"
	WITH_SERVICE=0
fi

if ! command -v curl >/dev/null 2>&1; then
	if command -v apk >/dev/null 2>&1; then
		log "installing curl + ca-certificates..."
		apk add --no-cache curl ca-certificates
	elif command -v apt-get >/dev/null 2>&1; then
		log "installing curl + ca-certificates..."
		apt-get update -qq
		apt-get install -y -qq curl ca-certificates
	else
		fail "curl is required but not installed"
	fi
fi

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
[ "$OS" = "linux" ] || fail "this script targets Linux (Debian/Ubuntu/Alpine); on macOS grab the darwin binary or use Docker"
case "$(uname -m)" in
	x86_64) ARCH="amd64" ;;
	aarch64 | arm64) ARCH="arm64" ;;
	*) fail "unsupported architecture: $(uname -m)" ;;
esac

if [ "$VERSION" = "latest" ]; then
	TAG="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep -o '"tag_name": *"[^"]*"' | cut -d'"' -f4)"
	[ -n "$TAG" ] || fail "could not resolve the latest release"
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
curl -fsSL "https://github.com/${REPO}/releases/download/${TAG}/${ASSET}" -o "${TMP}/${ASSET}"
curl -fsSL "https://github.com/${REPO}/releases/download/${TAG}/checksums.txt" -o "${TMP}/checksums.txt"
grep -F " ${ASSET}" "${TMP}/checksums.txt" >"${TMP}/expected.sha256"
(cd "$TMP" && sha256sum -c expected.sha256)
install -m 0755 "${TMP}/${ASSET}" "$INSTALL_PATH"
"$INSTALL_PATH" --version

if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
	log "creating user ${SERVICE_USER}..."
	if command -v useradd >/dev/null 2>&1; then
		useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
	else
		# Alpine busybox.
		adduser -S -D -H -s /sbin/nologin "$SERVICE_USER"
	fi
fi

# GNU and busybox su spell "run this as USER" differently; probe once.
if su -s /bin/sh "$SERVICE_USER" -c true >/dev/null 2>&1; then
	as_user() { su -s /bin/sh "$SERVICE_USER" -c "$1"; }
else
	as_user() { su -s /bin/sh "$SERVICE_USER" "$1"; }
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
	chown "$SERVICE_USER:$SERVICE_USER" "$r"
	if ! as_user "test -r '$r' -a -w '$r' -a -x '$r'"; then
		log "warning: ${SERVICE_USER} cannot read/write ${r}; linking (which moves files) will fail."
		log "warning: fix with e.g. 'chown -R ${SERVICE_USER}:${SERVICE_USER} ${r}' or pass --user <owner>."
	fi
done
chown -R "$SERVICE_USER:$SERVICE_USER" "$DATA_DIR"

if [ "$WITH_SERVICE" -eq 0 ]; then
	log "skipping service setup (--no-service or no supported init)."
	log "run manually: CODA_PORT=${PORT} CODA_ROOTS=${ROOTS} CODA_CONFIG=${DATA_DIR}/config.yaml CODA_DB_PATH=${DATA_DIR}/coda.db coda"
	exit 0
fi

if [ "$INIT" = "openrc" ]; then
	log "writing OpenRC service..."
	mkdir -p /etc/conf.d
	cat >"/etc/conf.d/${BIN_NAME}" <<EOF
CODA_PORT="${PORT}"
CODA_ROOTS="${ROOTS}"
CODA_CONFIG="${DATA_DIR}/config.yaml"
CODA_DB_PATH="${DATA_DIR}/coda.db"
GIN_MODE="release"
EOF
	cat >"/etc/init.d/${BIN_NAME}" <<EOF
#!/sbin/openrc-run
name="${BIN_NAME}"
description="Coda music library manager"
command="${INSTALL_PATH}"
command_user="${SERVICE_USER}:${SERVICE_USER}"
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
	chown "$SERVICE_USER:$SERVICE_USER" "/var/log/${BIN_NAME}.log"
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
Environment=CODA_PORT=${PORT}
Environment=CODA_ROOTS=${ROOTS}
Environment=CODA_CONFIG=${DATA_DIR}/config.yaml
Environment=CODA_DB_PATH=${DATA_DIR}/coda.db
Environment=GIN_MODE=release
ExecStart=${INSTALL_PATH}
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

	systemctl daemon-reload
	systemctl enable --now "$BIN_NAME"
	LOG_HINT="journalctl -u ${BIN_NAME} -f"
	FAIL_HINT="see 'journalctl -u ${BIN_NAME} -e'"
fi

log "waiting for health check..."
ok=0
for _ in $(seq 1 30); do
	if curl -fs "http://127.0.0.1:${PORT}/api/v1/health" 2>/dev/null | grep -q ok; then
		ok=1
		break
	fi
	sleep 1
done
[ "$ok" -eq 1 ] || fail "service did not become healthy; ${FAIL_HINT}"

IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
log ""
log "${BIN_NAME} ${TAG} is up: http://${IP:-localhost}:${PORT}"
log "config: ${DATA_DIR}/config.yaml   db: ${DATA_DIR}/coda.db"
log "logs:   ${LOG_HINT}"
log "upgrade: re-run this script (optionally with --version X.Y.Z)"
