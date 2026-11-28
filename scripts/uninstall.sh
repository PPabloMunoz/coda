#!/bin/sh
# Coda uninstaller for Linux (systemd or OpenRC).
# POSIX sh on purpose: runs on dash, busybox ash and bash.
#
# Usage:
#   sudo ./scripts/uninstall.sh
#   sudo ./scripts/uninstall.sh --remove-state --remove-user
#
# Removes the service definition and the /usr/local/bin/coda binary.
# State (DB + covers + config.yaml) and the service user are kept unless
# --remove-state / --remove-user are passed. Nothing is guessed: the state
# dir and the user are read from the installed service definition, or from
# --data-dir / --user when no service definition is installed.
set -eu

BIN_NAME="coda"
INSTALL_PATH="/usr/local/bin/coda"

REMOVE_STATE=0
REMOVE_USER=0
DATA_DIR=""
SERVICE_USER=""

usage() {
  cat <<'EOF'
Usage: uninstall.sh [options]

Removes the service definition and the coda binary. State and user are
kept unless asked for; re-run with the flags below to delete them.

Options:
  --remove-state   also delete the state dir (DB + covers + config.yaml)
  --remove-user    also delete the service user
  --data-dir DIR   state dir for --remove-state when no service definition
                   is installed to read it from
  --user USER      service user for --remove-user (default: read from the
                   installed service definition, else 'coda')
  --help           print this and exit
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
  --remove-state)
    REMOVE_STATE=1
    shift
    ;;
  --remove-user)
    REMOVE_USER=1
    shift
    ;;
  --data-dir)
    DATA_DIR="${2:?--data-dir needs a value}"
    shift 2
    ;;
  --user)
    SERVICE_USER="${2:?--user needs a value}"
    shift 2
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

[ "$(id -u)" -eq 0 ] || fail "run as root (e.g. 'sudo sh uninstall.sh')"

if [ -d /run/systemd/system ]; then
  INIT="systemd"
elif [ -d /run/openrc ] || have rc-service; then
  INIT="openrc"
else
  INIT="none"
fi

# The state dir and the user come from the installed service definition;
# --data-dir / --user override them (needed when none is installed).
UNIT_FILE="/etc/systemd/system/${BIN_NAME}.service"
CONFD_FILE="/etc/conf.d/${BIN_NAME}"
INITD_FILE="/etc/init.d/${BIN_NAME}"
if [ "$INIT" = "systemd" ] && [ -f "$UNIT_FILE" ]; then
  EXISTING_CONFIG="$(sed -n 's/^Environment="CODA_CONFIG=\(.*\)"/\1/p' "$UNIT_FILE" | head -n 1)"
  EXISTING_USER="$(sed -n 's/^User=\(.*\)$/\1/p' "$UNIT_FILE" | head -n 1)"
elif [ "$INIT" = "openrc" ] && [ -f "$CONFD_FILE" ]; then
  EXISTING_CONFIG="$(sed -n 's/^export CODA_CONFIG="\(.*\)"$/\1/p' "$CONFD_FILE" | head -n 1)"
  EXISTING_USER=""
  if [ -f "$INITD_FILE" ]; then
    EXISTING_USER="$(sed -n 's/^command_user="\([^":]*\).*/\1/p' "$INITD_FILE" | head -n 1)"
  fi
else
  EXISTING_CONFIG=""
  EXISTING_USER=""
fi

if [ -z "$DATA_DIR" ] && [ -n "$EXISTING_CONFIG" ]; then
  DATA_DIR="$(dirname "$EXISTING_CONFIG")"
fi
# Last resort when no service definition was found: the installer's default,
# and only when it actually exists. Anything else must be passed explicitly
# with --data-dir — never guessed.
if [ -z "$DATA_DIR" ] && [ -d /var/lib/coda ]; then
  DATA_DIR="/var/lib/coda"
fi
if [ -z "$SERVICE_USER" ]; then
  if [ -n "$EXISTING_USER" ]; then SERVICE_USER="$EXISTING_USER"; else SERVICE_USER="coda"; fi
fi

# Validate the destructive flags before touching anything: a refused flag
# must never leave a half-removed install behind.
if [ "$REMOVE_STATE" -eq 1 ]; then
  [ -n "$DATA_DIR" ] || fail "cannot determine the state dir (no service definition installed and no /var/lib/coda); pass --data-dir DIR"
  case "$DATA_DIR" in
  /*) ;;
  *) fail "--data-dir must be absolute: $DATA_DIR" ;;
  esac
  while [ "${DATA_DIR%/}" != "$DATA_DIR" ] && [ "$DATA_DIR" != "/" ]; do
    DATA_DIR="${DATA_DIR%/}"
  done
  case "$DATA_DIR" in
  /) fail "refusing to delete /" ;;
  /bin | /boot | /dev | /etc | /home | /lib | /lib64 | /media | /mnt | /opt | /proc | /root | /run | /sbin | /srv | /sys | /tmp | /usr | /var | /var/lib | /var/log)
    fail "refusing to delete '$DATA_DIR' (a system directory); delete what you need by hand"
    ;;
  esac
fi
if [ "$REMOVE_USER" -eq 1 ]; then
  case "$SERVICE_USER" in
  "") fail "--user must not be empty" ;;
  root) fail "--user must not be root (will not delete the root user)" ;;
  esac
fi

# Stop and disable the service first. The files are removed below even when
# the service was never enabled; only a running service that refuses to stop
# aborts the uninstall.
if [ "$INIT" = "systemd" ] && [ -f "$UNIT_FILE" ]; then
  log "disabling service..."
  systemctl disable --now "$BIN_NAME" || fail "cannot disable ${BIN_NAME}; see 'journalctl -u ${BIN_NAME} -e'"
elif [ "$INIT" = "openrc" ] && [ -f "$INITD_FILE" ]; then
  if rc-service "$BIN_NAME" status >/dev/null 2>&1; then
    log "stopping service..."
    rc-service "$BIN_NAME" stop || fail "cannot stop ${BIN_NAME}; see '/var/log/${BIN_NAME}.log'"
  fi
  rc-update del "$BIN_NAME" default 2>/dev/null ||
    log "note: ${BIN_NAME} was not in the default runlevel."
fi

log "removing files..."
rm -f "$INSTALL_PATH" \
  "/etc/systemd/system/${BIN_NAME}.service" \
  "/etc/conf.d/${BIN_NAME}" \
  "/etc/init.d/${BIN_NAME}" \
  "/etc/logrotate.d/${BIN_NAME}" \
  "/var/log/${BIN_NAME}.log" || fail "cannot remove installed files"
if [ "$INIT" = "systemd" ] && have systemctl; then
  systemctl daemon-reload || fail "systemctl daemon-reload failed"
fi

if [ "$REMOVE_STATE" -eq 1 ]; then
  if [ -e "$DATA_DIR" ]; then
    log "removing state dir ${DATA_DIR}..."
    rm -rf "$DATA_DIR" || fail "cannot remove ${DATA_DIR}"
  else
    log "state dir ${DATA_DIR} does not exist; nothing to remove."
  fi
fi

if [ "$REMOVE_USER" -eq 1 ]; then
  if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
    log "user ${SERVICE_USER} does not exist; nothing to remove."
  else
    log "removing user ${SERVICE_USER}..."
    if have userdel; then
      userdel "$SERVICE_USER" || fail "cannot delete user ${SERVICE_USER}"
    elif have deluser; then
      deluser "$SERVICE_USER" || fail "cannot delete user ${SERVICE_USER}"
    else
      fail "cannot delete user ${SERVICE_USER}: install 'userdel' first, or run 'userdel ${SERVICE_USER}' by hand"
    fi
  fi
fi

log ""
log "coda uninstalled."
if [ "$REMOVE_STATE" -eq 0 ]; then
  if [ -n "$DATA_DIR" ]; then
    log "kept state dir: ${DATA_DIR} (re-run with --remove-state to delete it)"
  else
    log "state dir unknown (no service definition was installed); remove it by hand if needed."
  fi
fi
if [ "$REMOVE_USER" -eq 0 ]; then
  log "kept user: ${SERVICE_USER} (re-run with --remove-user to delete it)"
fi
