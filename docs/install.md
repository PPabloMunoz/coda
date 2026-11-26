# Install

Coda ships as a single static binary with the web UI embedded. No external
dependencies, no database server.

| Method | Best for |
| --- | --- |
| [Install script](#install-script) | Linux servers with systemd or OpenRC — Debian/Ubuntu, Fedora/RHEL, openSUSE, Arch, Alpine, Void. E.g. a Proxmox LXC/VM. |
| [Docker Compose](#docker-compose) | Any Docker host |
| [Manual binary](#manual-binary) | macOS and Linux, when you manage the process yourself |
| [Build from source](#build-from-source) | Any OS, including Windows |

Releases exist for `linux/amd64`, `linux/arm64`, `darwin/amd64` and
`darwin/arm64`, plus a `checksums.txt`. There are no prebuilt Windows
binaries; build from source instead.

## Install script

Best for a Proxmox LXC/VM or any bare-metal Linux host. POSIX `sh`, needs
only `curl` or `wget` (installed for you via the system package manager
when missing):

```sh
curl -fsSL https://raw.githubusercontent.com/PPabloMunoz/coda/main/scripts/install.sh | sudo sh
```

With options (note the `-s --` separator when piping):

```sh
curl -fsSL https://raw.githubusercontent.com/PPabloMunoz/coda/main/scripts/install.sh \
  | sudo sh -s -- --roots /mnt/music --port 8687
```

The script downloads the latest release binary, verifies it against
`checksums.txt`, installs it to `/usr/local/bin/coda`, creates a `coda`
service user, writes a service definition (systemd unit, or OpenRC script
plus `conf.d` — env-only config, no YAML editing), and starts the service.
On hosts with neither init system it installs the binary and prints how to
run it by hand.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--version X.Y.Z` | latest | release to install |
| `--port PORT` | `8687` | port coda listens on |
| `--roots PATHS` | `/mnt/music` | music dirs, `:`-separated like `CODA_ROOTS` |
| `--data-dir DIR` | `/var/lib/coda` | state dir for DB, covers and config |
| `--user USER` | `coda` | service user to create/run as |
| `--no-service` | — | install the binary only |

The script warns when the service user cannot read/write a root — linking
moves files, so it needs write access.

**Upgrade:** re-run the script. It keeps the previous port, roots, data
dir and user; a flag you pass overrides them. Back up first on 0.x (see
[operations.md](operations.md#backup-and-restore)).

**Uninstall:**

```sh
sudo systemctl disable --now coda          # or: rc-service coda stop; rc-update del coda
sudo rm -f /usr/local/bin/coda /etc/systemd/system/coda.service /etc/conf.d/coda /etc/init.d/coda
sudo userdel coda                          # if you don't want the user anymore
sudo rm -rf /var/lib/coda                  # state: DB + covers + config
```

## Docker Compose

```sh
mkdir -p music data
PUID=$(id -u) PGID=$(id -g) docker compose up -d
curl localhost:8687/api/v1/health
```

The compose file uses `ghcr.io/ppablomunoz/coda` and mounts `music/` and
`data/` (config, DB and covers live under `/data` inside the container).
The published port binds to loopback by default (`127.0.0.1:8687`):
Coda has no authentication, so it is only reachable from the Docker host
unless you opt out:

```sh
CODA_BIND_ADDR=0.0.0.0 docker compose up -d   # trusted LANs only
```

Files Coda moves or tags are written as `PUID:PGID`, Lidarr-style — the
host dirs must be owned by those ids, or the scan fails and linking cannot
move files. If you point `MUSIC_DIR` at an existing library, chown it
first:

```sh
sudo chown -R "$(id -u):$(id -g)" /path/to/library
```

**Upgrade:** `docker compose pull && docker compose up -d`.
**Logs:** `docker compose logs -f`.
**Uninstall:** `docker compose down` and remove the `music/` and `data/`
dirs when you're done with the state.

## Manual binary

Download the binary for your platform plus `checksums.txt` from
[GitHub Releases](https://github.com/PPabloMunoz/coda/releases), verify
it, and put `coda` on your `PATH`:

```sh
sha256sum -c --ignore-missing checksums.txt        # GNU coreutils
shasum -a 256 -c --ignore-missing checksums.txt    # macOS
chmod +x coda-linux-amd64
sudo mv coda-linux-amd64 /usr/local/bin/coda
```

`--ignore-missing` matters: `checksums.txt` lists all four platform
binaries, and you only downloaded one. Run `coda --version` to confirm,
then see [configuration.md](configuration.md) for config and env.

Run it under your init system of choice, or a plain `systemd` unit
equivalent to what the installer writes (env-only config: `CODA_HOST`,
`CODA_PORT`, `CODA_ROOTS`, `CODA_CONFIG`, `CODA_DB_PATH`). Set
`CODA_HOST=127.0.0.1` to listen on loopback only.

## Build from source

Requires [Go](https://go.dev) (developed with 1.27.3) — no cgo, no
toolchain beyond Go:

```sh
git clone https://github.com/PPabloMunoz/coda
cd coda
go build -o coda ./cmd/coda          # dev build
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o coda ./cmd/coda
```

Or with [just](https://just.systems/): `just build`, `just build-prod`,
`just run`. See [CONTRIBUTING.md](../CONTRIBUTING.md) for the full
development setup. Windows builds work the same way (`go build`); run the
binary and configure via `config.yaml` or env.

## First run

Coda creates `config.yaml` on first start (see
[configuration.md](configuration.md#config-file) for the per-OS path).
Point `roots` at your library, validate with `coda --check-config`, then
open `http://localhost:8687`:

1. Add artists by MusicBrainz ID — each import queues a background
   discography import.
2. Open **Link** to attach unlinked files to release tracks. Linking
   writes standard tags, embeds the cover, and moves the file into
   `<root>/<Artist>/<Album>/` (see [library.md](library.md)).
