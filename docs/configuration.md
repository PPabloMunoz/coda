# Configuration

Coda reads a small YAML file plus environment variables. Precedence:
command-line flags, then environment, then the config file.

## Config file

Created automatically on first run with defaults (`port: 8687`, empty
`roots`):

- macOS: `~/Library/Application Support/Coda/config.yaml`
- Linux: `~/.config/coda/config.yaml`
- Windows (build from source): `%APPDATA%\Coda\config.yaml`

Override the location with `CODA_CONFIG` or `coda --config /path/to/config.yaml`.

```yaml
port: 8687
roots:
  - /absolute/path/to/music
```

Validation: `port` must be 1–65535; each `roots` entry must be an absolute
path to an existing directory; duplicates and nested roots (one inside
another) are rejected; symlinks are resolved to their real path.

## Environment variables

| Variable | Meaning |
| --- | --- |
| `CODA_CONFIG` | config file path |
| `CODA_PORT` | server port (1–65535), wins over the file |
| `CODA_ROOTS` | music roots, **replaces** the file's `roots`. OS path list: `:`-separated on macOS/Linux, `;`-separated on Windows. Empty entries are dropped. |
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
port: 8687
roots: 2
  - /music
  - /other
db: /home/you/.local/share/coda/coda.db
covers: /home/you/.local/share/coda/covers
```

## Docker Compose variables

The compose file reads:

| Variable | Default | Meaning |
| --- | --- | --- |
| `PUID` / `PGID` | `1000` | user/group the container runs as; must own `music/` and `data/` on the host |
| `MUSIC_DIR` | `./music` | host path mounted at `/music` |
| `DATA_DIR` | `./data` | host path mounted at `/data` (config, DB, covers) |
| `CODA_PORT` | `8687` | published port |
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
