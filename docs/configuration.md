# Configuration

Coda reads a small YAML file plus environment variables. Precedence:
command-line flags, then environment, then the config file.

## Config file

Created automatically on first run with defaults (empty `host`, `port:
8687`, empty `roots`):

- macOS: `~/Library/Application Support/Coda/config.yaml`
- Linux: `~/.config/coda/config.yaml`
- Windows (build from source): `%APPDATA%\Coda\config.yaml`

Override the location with `CODA_CONFIG` or `coda --config /path/to/config.yaml`.

```yaml
# host: empty = all interfaces (default). Set to 127.0.0.1 to listen on
# loopback only — recommended when Coda is not behind a reverse proxy.
host: ""
port: 8687
roots:
  - /absolute/path/to/music
# allowed_hosts: extra Host names accepted besides localhost, IP literals,
# and the host above (reverse proxies, mDNS names, Docker hostnames).
# Empty by default; ports are ignored when matching.
allowed_hosts: []
# scan_timeout: bounds the whole library scan (walk + sync + auto-link +
# prune) as a Go duration. Default 10m; increase for large or slow
# (network) libraries.
scan_timeout: 10m
```

Validation: `host` must be empty or a bare hostname/IP (`127.0.0.1`,
`::1`); `host:port` values are rejected — the port belongs in `port`.
`port` must be 1–65535; `scan_timeout` must be a positive Go duration (`10m`, `15m`, `1h`); each `roots` entry must be an absolute
path to an existing directory; duplicates and nested roots (one inside
another) are rejected; symlinks are resolved to their real path.
`allowed_hosts` entries must be bare hostnames/IPs (lowercased, trailing
dots stripped, duplicates collapsed); empty entries, `*`, URLs, paths,
and `host:port` values are rejected.

### Host allowlist

Coda has no authentication, so it checks the `Host` header as DNS-rebinding
mitigation. A request is accepted when its host (port ignored) is:

- `localhost`, any IP literal (v4/v6 — healthchecks and LAN IPs keep
  working), or the configured `host` (unless it is empty or a wildcard
  bind like `0.0.0.0`/`::`, which adds nothing), or
- a name in `allowed_hosts` / `CODA_ALLOWED_HOSTS`.

Anything else answers `421 INVALID_HOST` with the standard error envelope.
Behind a reverse proxy, add the public hostname to `allowed_hosts`:

```yaml
host: "127.0.0.1"
allowed_hosts:
  - coda.example.com
```

Direct-IP access never needs listing. If the UI shows `421`, add the name
you browse by to `allowed_hosts` (see
[operations.md](operations.md#http-421-host-not-allowed)).

## Environment variables

| Variable | Meaning |
| --- | --- |
| `CODA_CONFIG` | config file path |
| `CODA_HOST` | listen address: bare hostname or IP (`127.0.0.1` for loopback only). Empty keeps the file's `host`; empty file value means all interfaces. |
| `CODA_PORT` | server port (1–65535), wins over the file |
| `CODA_ROOTS` | music roots, **replaces** the file's `roots`. OS path list: `:`-separated on macOS/Linux, `;`-separated on Windows. Empty entries are dropped. |
| `CODA_ALLOWED_HOSTS` | extra Host-allowlist names, **replaces** the file's `allowed_hosts`. Comma-separated bare hostnames/IPs (`coda.lan,nas.local`); surrounding spaces tolerated, empty entries dropped. Empty keeps the file's list. |
| `CODA_SCAN_TIMEOUT` | scan bound as Go duration (`15m`), wins over the file; empty keeps the file's `scan_timeout` (default `10m`) |
| `CODA_DB_PATH` | SQLite database path (the `covers/` cache dir, `coda.db.lock` and pre-migration `coda.db.bak-*` sidecars all land next to it) |
| `CODA_DEBUG` | `1`/`true`/`yes`/`on` enables debug logs on stderr |
| `CODA_LOG_LEVEL` | set to `debug` for the same effect |
| `GIN_MODE` | `debug` opts back into Gin's debug route dump; release is the default |

```sh
CODA_PORT=8687 CODA_ROOTS=/music:/other coda --check-config
```

## `--check-config`

Prints the resolved configuration and exits without starting the server —
use it to validate a new setup or container environment:

```sh
$ coda --check-config
coda 0.3.1
host: 127.0.0.1
port: 8687
listen: 127.0.0.1:8687
roots: 2
  - /music
  - /other
allowed_hosts: 1
  - coda.lan
scan_timeout: 10m0s
db: /home/you/.local/share/coda/coda.db
covers: /home/you/.local/share/coda/covers
lock: /home/you/.local/share/coda/coda.db.lock
```

An empty `host` prints `host: (all interfaces)` and `listen: :8687`.

## Docker Compose variables

The compose file reads:

| Variable | Default | Meaning |
| --- | --- | --- |
| `PUID` / `PGID` | `1000` | user/group the container runs as; must own `music/` and `data/` on the host |
| `MUSIC_DIR` | `./music` | host path mounted at `/music` |
| `DATA_DIR` | `./data` | host path mounted at `/data` (config, DB, covers) |
| `CODA_PORT` | `8687` | host-side port of the published mapping |
| `CODA_BIND_ADDR` | `127.0.0.1` | host interface to publish on; `0.0.0.0` exposes Coda to the LAN (trusted networks only — Coda has no authentication) |
| `CODA_TAG` | `latest` | image tag (`latest`, `0.3`, `0.3.1`) |

## Installer flags

`scripts/install.sh` accepts `--version`, `--port`, `--roots`,
`--data-dir`, `--user`, `--no-service`, `--help`. Defaults: port `8687`,
data dir `/var/lib/coda`, user `coda`; roots default to none (the app
creates a default config on first run — add roots later or pass `--roots`).
Passed roots must already exist (service installs only); the installer never
creates or chowns library directories — the service user needs read/write on
them, which you arrange yourself. `--no-service` installs the binary only. There are no
`--host` / `--allowed-hosts` flags: to bind loopback, add
`CODA_HOST=127.0.0.1` (plus `CODA_ALLOWED_HOSTS` for the public name) to the
service definition by hand after installing; upgrades keep those lines.
Re-running the installer keeps the previous settings; a flag you pass
overrides the corresponding setting.

## Data locations

| | Config | Database + covers |
| --- | --- | --- |
| macOS | `~/Library/Application Support/Coda/config.yaml` | same dir |
| Linux | `~/.config/coda/config.yaml` | `~/.local/share/coda/` (or `$XDG_DATA_HOME/coda/`) |
| Windows | `%APPDATA%\Coda\config.yaml` | `%LOCALAPPDATA%\Coda\` |
| Docker | `/data/config.yaml` | `/data/` |
| Installer | `/var/lib/coda/config.yaml` | `/var/lib/coda/` |

The cover cache is a `covers/` directory next to the database file: one
file per release (`<release-mbid>.jpg|png|webp`) plus `artist-<mbid>.*`
photos. Files orphaned by deletes are swept on the next scan. The
`coda.db.lock` single-instance lock and automatic `coda.db.bak-*`
pre-migration snapshots live next to the database too. See
[operations.md](operations.md) for backups.
