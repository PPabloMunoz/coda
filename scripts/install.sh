#!/bin/sh
# Coda installer for Linux (systemd or OpenRC) on amd64 and arm64.
# POSIX sh on purpose: runs on dash, busybox ash and bash.
#
# One-liner:
#   curl -fsSL https://raw.githubusercontent.com/PPabloMunoz/coda/main/scripts/install.sh | sudo sh
# With options (note the `-s --` separator when piping):
#   curl -fsSL .../scripts/install.sh | sudo sh -s -- --roots /mnt/music --port 8687
#   sudo ./scripts/install.sh --version 0.3.1 --roots /mnt/music:/mnt/other
#
# What it does: downloads the release binary + checksums.txt, verifies the
# checksum, installs to /usr/local/bin/coda. Unless --no-service is passed,
# it also creates the service user, writes a service definition (systemd unit
# or OpenRC script + conf.d), and starts the service.
#
# What it does NOT do: fix your system. A missing tool, a missing music dir,
# a bad flag, or a permission problem is reported as an error — you fix it
# and re-run. Re-running upgrades the binary in place and keeps the previous
# port, roots, data dir and user (a flag you pass overrides them).
#
# Coda has no authentication and listens on all interfaces by default: only
# run it on trusted LANs, or set CODA_HOST=127.0.0.1 in the service
# definition by hand and put it behind a reverse proxy (see docs/install.md).
set -eu

REPO="PPabloMunoz/coda"
BIN_NAME="coda"
INSTALL_PATH="/usr/local/bin/coda"

VERSION="latest"
# Empty means "not passed": on re-run the value is kept from the previously
# installed service definition, falling back to the built-in defaults.
PORT=""
ROOTS=""
DATA_DIR=""
SERVICE_USER=""
WITH_SERVICE=1

usage() {
  cat <<'EOF'
Usage: install.sh [options]

Re-running upgrades in place and keeps the previous port, roots, data dir
and user; a flag you pass overrides it.

Options:
  --version X.Y.Z   release to install (default: latest stable)
  --port PORT       port coda listens on (default: 8687)
  --roots PATHS     music dirs, ':'-separated like CODA_ROOTS (default: none;
                    add them later via config.yaml or re-run with --roots)
  --data-dir DIR    state dir for DB, covers and config.yaml (default: /var/lib/coda)
  --user USER       service user to create/run as (default: coda, never root)
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

# True when $1 contains a newline; such values can never be written to the
# service definition, so they are rejected up front.
has_newline() {
  case "$1" in
  *"
"*) return 0 ;;
  *) return 1 ;;
  esac
}

# True when $1 contains a character that would break the systemd unit or the
# conf.d file. The installer does not escape anything — it rejects these.
has_bad_char() {
  case "$1" in
  *\"* | *\\* | *'$'* | *'`'* | *%*) return 0 ;;
  *) return 1 ;;
  esac
}

# Reject one flag value ($1=name, $2=value) before anything else happens.
check_value() {
  if has_newline "$2"; then
    fail "$1 must not contain a newline"
  fi
  if has_bad_char "$2"; then
    fail "$1 must not contain '\"', '\\', '\$', backtick or '%'"
  fi
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

# Keep existing settings on re-run: a flag you omit falls back to the value
# in the installed service definition, then to the built-in default.
# CODA_HOST / CODA_ALLOWED_HOSTS have no installer flags; when an existing
# service definition sets them (added by hand), they are carried forward
# verbatim so an upgrade never silently widens what the service listens on.
resolve_settings() {
  UNIT_FILE="/etc/systemd/system/${BIN_NAME}.service"
  CONFD_FILE="/etc/conf.d/${BIN_NAME}"
  INITD_FILE="/etc/init.d/${BIN_NAME}"
  EXISTING_PORT=""
  EXISTING_ROOTS=""
  EXISTING_CONFIG=""
  EXISTING_DB=""
  EXISTING_USER=""
  EXISTING_HOST=""
  EXISTING_ALLOWED_HOSTS=""

  if [ "$INIT" = "systemd" ] && [ -f "$UNIT_FILE" ]; then
    EXISTING_PORT="$(sed -n 's/^Environment="CODA_PORT=\(.*\)"/\1/p' "$UNIT_FILE" | head -n 1)"
    EXISTING_ROOTS="$(sed -n 's/^Environment="CODA_ROOTS=\(.*\)"/\1/p' "$UNIT_FILE" | head -n 1)"
    EXISTING_CONFIG="$(sed -n 's/^Environment="CODA_CONFIG=\(.*\)"/\1/p' "$UNIT_FILE" | head -n 1)"
    EXISTING_DB="$(sed -n 's/^Environment="CODA_DB_PATH=\(.*\)"/\1/p' "$UNIT_FILE" | head -n 1)"
    EXISTING_USER="$(sed -n 's/^User=\(.*\)$/\1/p' "$UNIT_FILE" | head -n 1)"
    EXISTING_HOST="$(sed -n 's/^Environment="CODA_HOST=\(.*\)"/\1/p' "$UNIT_FILE" | head -n 1)"
    EXISTING_ALLOWED_HOSTS="$(sed -n 's/^Environment="CODA_ALLOWED_HOSTS=\(.*\)"/\1/p' "$UNIT_FILE" | head -n 1)"
  elif [ "$INIT" = "openrc" ] && [ -f "$CONFD_FILE" ]; then
    EXISTING_PORT="$(sed -n 's/^export CODA_PORT="\(.*\)"$/\1/p' "$CONFD_FILE" | head -n 1)"
    EXISTING_ROOTS="$(sed -n 's/^export CODA_ROOTS="\(.*\)"$/\1/p' "$CONFD_FILE" | head -n 1)"
    EXISTING_CONFIG="$(sed -n 's/^export CODA_CONFIG="\(.*\)"$/\1/p' "$CONFD_FILE" | head -n 1)"
    EXISTING_DB="$(sed -n 's/^export CODA_DB_PATH="\(.*\)"$/\1/p' "$CONFD_FILE" | head -n 1)"
    EXISTING_HOST="$(sed -n 's/^export CODA_HOST="\(.*\)"$/\1/p' "$CONFD_FILE" | head -n 1)"
    EXISTING_ALLOWED_HOSTS="$(sed -n 's/^export CODA_ALLOWED_HOSTS="\(.*\)"$/\1/p' "$CONFD_FILE" | head -n 1)"
    if [ -f "$INITD_FILE" ]; then
      EXISTING_USER="$(sed -n 's/^command_user="\([^":]*\).*/\1/p' "$INITD_FILE" | head -n 1)"
    fi
  fi

  if [ -z "$PORT" ]; then
    if [ -n "$EXISTING_PORT" ]; then PORT="$EXISTING_PORT"; else PORT="8687"; fi
  fi
  if [ -z "$ROOTS" ]; then
    # No default: the app starts fine with no roots (it creates a default
    # config on first run); add them later via config.yaml or --roots.
    ROOTS="$EXISTING_ROOTS"
  fi
  if [ -z "$DATA_DIR" ]; then
    if [ -n "$EXISTING_CONFIG" ]; then
      DATA_DIR="$(dirname "$EXISTING_CONFIG")"
    else
      DATA_DIR="/var/lib/coda"
    fi
  fi
  if [ -z "$SERVICE_USER" ]; then
    if [ -n "$EXISTING_USER" ]; then SERVICE_USER="$EXISTING_USER"; else SERVICE_USER="coda"; fi
  fi

  # Config and DB always live together under the data dir. A manually split
  # state is left alone: fail instead of silently orphaning one of them.
  if [ -n "$EXISTING_CONFIG" ] && [ -n "$EXISTING_DB" ]; then
    if [ "$(dirname "$EXISTING_CONFIG")" != "$(dirname "$EXISTING_DB")" ]; then
      fail "existing CODA_CONFIG ($EXISTING_CONFIG) and CODA_DB_PATH ($EXISTING_DB) are in different dirs; pass --data-dir to consolidate, or fix them by hand"
    fi
  fi
}
resolve_settings

# Validate every flag before touching the network or the installed binary.
check_value "--port" "$PORT"
check_value "--roots" "$ROOTS"
check_value "--data-dir" "$DATA_DIR"
check_value "--user" "$SERVICE_USER"
check_value "--version" "$VERSION"

case "$VERSION" in
latest) ;;
*)
  ver="$VERSION"
  case "$ver" in
  v*) ver="${ver#v}" ;;
  esac
  case "$ver" in
  "" | *[!0-9A-Za-z._-]* | */*) fail "invalid --version '$VERSION' (expected 'latest' or like 0.3.0)" ;;
  esac
  case "$ver" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) fail "invalid --version '$VERSION' (expected 'latest' or like 0.3.0)" ;;
  esac
  ;;
esac

case "$PORT" in
"" | *[!0-9]*) fail "invalid --port '$PORT' (expected 1-65535)" ;;
esac
if [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
  fail "invalid --port '$PORT' (expected 1-65535)"
fi

# Empty roots are fine (the app creates a default config on first run).
# When passed, every entry must be an absolute path.
if [ -n "$ROOTS" ]; then
  case "$ROOTS" in
  :* | *: | *::*) fail "empty entry in --roots (check for '::' or leading/trailing ':')" ;;
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
  done
fi

case "$DATA_DIR" in
/*) ;;
*) fail "--data-dir must be absolute: $DATA_DIR" ;;
esac
while [ "${DATA_DIR%/}" != "$DATA_DIR" ] && [ "$DATA_DIR" != "/" ]; do
  DATA_DIR="${DATA_DIR%/}"
done
[ "$DATA_DIR" = "/" ] && fail "--data-dir must not be /"

case "$SERVICE_USER" in
"") fail "--user must not be empty" ;;
root) fail "--user must not be root (the service must not run as root)" ;;
esac
if [ "${#SERVICE_USER}" -gt 32 ]; then
  fail "invalid --user '$SERVICE_USER' (max 32 characters)"
fi
case "$SERVICE_USER" in
*[!A-Za-z0-9_.-]* | [-.0-9]*) fail "invalid --user '$SERVICE_USER' (letters, digits, '_', '-', '.'; must not start with '-', '.' or a digit)" ;;
esac

# Dependencies are reported, not installed. Install the missing one and re-run.
if ! have curl && ! have wget; then
  fail "need 'curl' or 'wget' installed (e.g. 'apt install curl')"
fi
if ! have sha256sum && ! have shasum; then
  fail "need 'sha256sum' or 'shasum' installed to verify the download"
fi

if have curl; then
  fetch() { curl -fsSL "$1" -o "$2"; }
  fetch_stdout() { curl -fsSL "$1"; }
else
  fetch() { wget -q -O "$2" "$1"; }
  fetch_stdout() { wget -q -O - "$1"; }
fi

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
fetch "https://github.com/${REPO}/releases/download/${TAG}/${ASSET}" "${TMP}/${ASSET}" ||
  fail "download failed for ${ASSET} (check the version and your network)"
fetch "https://github.com/${REPO}/releases/download/${TAG}/checksums.txt" "${TMP}/checksums.txt" ||
  fail "download failed for checksums.txt (check the version and your network)"
# Exact filename match (checksums.txt lines are "<hash>  <name>" or "<hash> *<name>").
awk -v a="$ASSET" '$2 == a || $2 == "*" a { print; found = 1 } END { exit !found }' \
  "${TMP}/checksums.txt" >"${TMP}/expected.sha256" || fail "no checksum entry for ${ASSET}"
if have sha256sum; then
  (cd "$TMP" && sha256sum -c expected.sha256) || fail "checksum verification failed for ${ASSET}"
elif have shasum; then
  (cd "$TMP" && shasum -a 256 -c expected.sha256) || fail "checksum verification failed for ${ASSET}"
fi

CONFIG_PATH="${DATA_DIR}/config.yaml"
DB_PATH="${DATA_DIR}/coda.db"

# Binary-only mode: no user, data dir, roots checks, or service setup.
if [ "$WITH_SERVICE" -eq 0 ]; then
  mkdir -p "$(dirname "$INSTALL_PATH")" || fail "cannot create $(dirname "$INSTALL_PATH")"
  install -m 0755 "${TMP}/${ASSET}" "$INSTALL_PATH" || fail "cannot install to $INSTALL_PATH"
  if [ "$INIT" = "none" ]; then
    log "no supported init system found (need systemd or OpenRC); installed the binary only."
  else
    log "installed the binary only (--no-service); no user, data dir, or service was set up."
  fi
  manual="CODA_PORT=${PORT}"
  if [ -n "$ROOTS" ]; then
    manual="$manual CODA_ROOTS=${ROOTS}"
  fi
  manual="$manual CODA_CONFIG=${CONFIG_PATH} CODA_DB_PATH=${DB_PATH} ${INSTALL_PATH}"
  log "run manually: $manual"
  log "after changing config: stop coda and run the command above again."
  log ""
  log "created:"
  log "  binary:   ${INSTALL_PATH}"
  log "on first start the app writes state under ${DATA_DIR}:"
  log "  config:   ${CONFIG_PATH}"
  log "  database: ${DB_PATH}"
  log "  covers:   ${DATA_DIR}/covers"
  exit 0
fi

# --- Service installs only from here on. ---

# Roots must already exist when given: Coda never creates library directories.
if [ -n "$ROOTS" ]; then
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
    [ -d "$r" ] || fail "root does not exist: $r (create or mount it first, then re-run)"
  done
fi

if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
  log "creating user ${SERVICE_USER}..."
  NOLOGIN="$(command -v nologin 2>/dev/null || true)"
  [ -n "$NOLOGIN" ] || NOLOGIN="/bin/false"
  if have useradd; then
    getent group "$SERVICE_USER" >/dev/null 2>&1 || groupadd --system "$SERVICE_USER"
    useradd --system --no-create-home --shell "$NOLOGIN" -g "$SERVICE_USER" "$SERVICE_USER" ||
      fail "cannot create user ${SERVICE_USER}"
  elif have adduser; then
    getent group "$SERVICE_USER" >/dev/null 2>&1 || addgroup -S "$SERVICE_USER"
    adduser -S -D -H -G "$SERVICE_USER" -s "$NOLOGIN" "$SERVICE_USER" ||
      fail "cannot create user ${SERVICE_USER}"
  else
    fail "cannot create user ${SERVICE_USER}: install 'useradd' or 'adduser' first"
  fi
fi
# --user may name an existing account whose primary group differs from its name.
SERVICE_GROUP="$(id -gn "$SERVICE_USER")" || fail "cannot read primary group of ${SERVICE_USER}"

mkdir -p "$DATA_DIR" || fail "cannot create $DATA_DIR"
chown "$SERVICE_USER:$SERVICE_GROUP" "$DATA_DIR" || fail "cannot chown $DATA_DIR to ${SERVICE_USER}:${SERVICE_GROUP}"
chmod 0750 "$DATA_DIR" || fail "cannot chmod $DATA_DIR"

# The binary goes in only after every check above passed, so an invalid flag
# or a missing root never leaves a new binary behind with an old service config.
mkdir -p "$(dirname "$INSTALL_PATH")" || fail "cannot create $(dirname "$INSTALL_PATH")"
install -m 0755 "${TMP}/${ASSET}" "$INSTALL_PATH" || fail "cannot install to $INSTALL_PATH"

if [ "$INIT" = "openrc" ]; then
  log "writing OpenRC service..."
  mkdir -p /etc/conf.d || fail "cannot create /etc/conf.d"
  {
    printf 'export CODA_PORT="%s"\n' "$PORT"
    if [ -n "$ROOTS" ]; then
      printf 'export CODA_ROOTS="%s"\n' "$ROOTS"
    fi
    printf 'export CODA_CONFIG="%s"\n' "$CONFIG_PATH"
    printf 'export CODA_DB_PATH="%s"\n' "$DB_PATH"
    printf 'export GIN_MODE="release"\n'
    if [ -n "$EXISTING_HOST" ]; then
      printf 'export CODA_HOST="%s"\n' "$EXISTING_HOST"
    fi
    if [ -n "$EXISTING_ALLOWED_HOSTS" ]; then
      printf 'export CODA_ALLOWED_HOSTS="%s"\n' "$EXISTING_ALLOWED_HOSTS"
    fi
  } >"/etc/conf.d/${BIN_NAME}" || fail "cannot write /etc/conf.d/${BIN_NAME}"
  cat >"/etc/init.d/${BIN_NAME}" <<EOF || fail "cannot write /etc/init.d/${BIN_NAME}"
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
  chmod +x "/etc/init.d/${BIN_NAME}" || fail "cannot chmod /etc/init.d/${BIN_NAME}"
  touch "/var/log/${BIN_NAME}.log" || fail "cannot create /var/log/${BIN_NAME}.log"
  chown "$SERVICE_USER:$SERVICE_GROUP" "/var/log/${BIN_NAME}.log" ||
    fail "cannot chown /var/log/${BIN_NAME}.log to ${SERVICE_USER}:${SERVICE_GROUP}"
  log "writing logrotate config..."
  mkdir -p /etc/logrotate.d || fail "cannot create /etc/logrotate.d"
  cat >"/etc/logrotate.d/${BIN_NAME}" <<EOF || fail "cannot write /etc/logrotate.d/${BIN_NAME}"
/var/log/${BIN_NAME}.log {
    weekly
    rotate 4
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
}
EOF
  if ! have logrotate; then
    log "note: logrotate is not installed; install it or /var/log/${BIN_NAME}.log will grow without bound."
  fi
  rc-update add "$BIN_NAME" default || fail "cannot add ${BIN_NAME} to the default runlevel"
  if rc-service "$BIN_NAME" status >/dev/null 2>&1; then
    rc-service "$BIN_NAME" restart || fail "failed to restart ${BIN_NAME}; see '/var/log/${BIN_NAME}.log'"
  else
    rc-service "$BIN_NAME" start || fail "failed to start ${BIN_NAME}; see '/var/log/${BIN_NAME}.log'"
  fi
  LOG_HINT="tail -f /var/log/${BIN_NAME}.log"
  FAIL_HINT="see '/var/log/${BIN_NAME}.log'"
else
  log "writing systemd unit..."
  {
    printf '[Unit]\n'
    printf 'Description=Coda music library manager\n'
    printf 'After=network-online.target\n'
    printf 'Wants=network-online.target\n'
    printf '\n[Service]\n'
    printf 'User=%s\n' "$SERVICE_USER"
    printf 'Group=%s\n' "$SERVICE_GROUP"
    printf 'Environment="CODA_PORT=%s"\n' "$PORT"
    if [ -n "$ROOTS" ]; then
      printf 'Environment="CODA_ROOTS=%s"\n' "$ROOTS"
    fi
    printf 'Environment="CODA_CONFIG=%s"\n' "$CONFIG_PATH"
    printf 'Environment="CODA_DB_PATH=%s"\n' "$DB_PATH"
    printf 'Environment="GIN_MODE=release"\n'
    if [ -n "$EXISTING_HOST" ]; then
      printf 'Environment="CODA_HOST=%s"\n' "$EXISTING_HOST"
    fi
    if [ -n "$EXISTING_ALLOWED_HOSTS" ]; then
      printf 'Environment="CODA_ALLOWED_HOSTS=%s"\n' "$EXISTING_ALLOWED_HOSTS"
    fi
    printf 'ExecStart=%s\n' "$INSTALL_PATH"
    printf 'Restart=on-failure\n'
    printf 'RestartSec=5\n'
    printf 'NoNewPrivileges=true\n'
    printf '\n[Install]\n'
    printf 'WantedBy=multi-user.target\n'
  } >"/etc/systemd/system/${BIN_NAME}.service" || fail "cannot write /etc/systemd/system/${BIN_NAME}.service"

  systemctl daemon-reload || fail "systemctl daemon-reload failed"
  systemctl enable "$BIN_NAME" || fail "systemctl enable ${BIN_NAME} failed"
  # restart (not just start) so re-running the script picks up the new binary.
  systemctl restart "$BIN_NAME" || fail "failed to restart ${BIN_NAME}; see 'journalctl -u ${BIN_NAME} -e'"
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
log "warning: Coda has no authentication and listens on all interfaces; trusted LANs only."
log "warning: to bind loopback instead, set CODA_HOST=127.0.0.1 in the service definition and restart."
if [ -z "$ROOTS" ]; then
  log "no music roots configured yet: point roots at your library (config.yaml, or re-run with --roots ...) and rescan."
else
  log "make sure ${SERVICE_USER} can read/write your music dirs (linking moves files)."
fi
log ""
log "created:"
log "  binary:   ${INSTALL_PATH}"
log "  user:     ${SERVICE_USER} (group ${SERVICE_GROUP})"
if [ "$INIT" = "openrc" ]; then
  log "  init:     /etc/init.d/${BIN_NAME}"
  log "  conf:     /etc/conf.d/${BIN_NAME}"
  log "  logrotate: /etc/logrotate.d/${BIN_NAME}"
  log "  log file: /var/log/${BIN_NAME}.log"
else
  log "  unit:     /etc/systemd/system/${BIN_NAME}.service"
fi
log "  data dir: ${DATA_DIR}"
log "  config:   ${CONFIG_PATH} (written on first start)"
log "  database: ${DB_PATH} (created on first start)"
log "  covers:   ${DATA_DIR}/covers (created as needed)"
if [ -n "$ROOTS" ]; then
  log "  roots:    ${ROOTS}"
else
  log "  roots:    none configured"
fi
log "  logs:     ${LOG_HINT}"
if [ "$INIT" = "openrc" ]; then
  log "  restart:  sudo rc-service ${BIN_NAME} restart   # after changing config.yaml or the service definition"
else
  log "  restart:  sudo systemctl restart ${BIN_NAME}   # after changing config.yaml or the service definition"
fi
log "upgrade: re-run this script (optionally with --version X.Y.Z)"
