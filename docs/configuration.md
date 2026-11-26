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
```

Validation: `host` must be empty or a bare hostname/IP (`127.0.0.1`,
`::1`); `host:port` values are rejected — the port belongs in `port`.
`port` must be 1–65535; each `roots` entry must be an absolute
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
| `CODA_DB_PATH` | SQLite database path (the `covers/` cache dir lands next to it) |
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
coda 0.2.0
host: 127.0.0.1
port: 8687
listen: 127.0.0.1:8687
roots: 2
  - /music
  - /other
allowed_hosts: 1
  - coda.lan
db: /home/you/.local/share/coda/coda.db
covers: /home/you/.local/share/coda/covers
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
| `CODA_TAG` | `latest` | image tag (`latest`, `0.2`, `0.2.0`) |

## Installer flags

`scripts/install.sh` accepts `--version`, `--port`, `--roots`,
`--data-dir`, `--user`, `--no-service`, `--help`. Defaults: port `8687`,
roots `/mnt/music`, data dir `/var/lib/coda`, user `coda`. Re-running the
installer keeps the previous settings; a flag you pass overrides them.

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
photos. See [operations.md](operations.md) for backups.
