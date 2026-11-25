<div align="center">
  <img src="docs/assets/logo.png" alt="Coda logo" width="128" />
  <h1>Coda</h1>
  <p><strong>Self-hosted music library management, done carefully.</strong></p>
  <p>Accurate metadata first. Automation only when it's trustworthy.</p>
</div>

> Coda `0.1.0` is the first release — early prototype, expect breaking changes in `0.x`.

## What is Coda

Coda is open-source, self-hosted music library management software. It takes
inspiration from [Lidarr](https://lidarr.audio/),
[MusicBrainz Picard](https://picard.musicbrainz.org/), and the self-hosted
music ecosystem (e.g. Navidrome) — with a narrower focus: a library and
metadata you can trust.

Coda scans audio files from your configured folders into a local SQLite
database and serves them over a small HTTP API plus an embedded web UI.
Scanning is read-only: ambiguous matches are surfaced for review, never
guessed silently. Tags are written only when you explicitly link/unlink a
file to a release track.

## Install

### Install script (Debian/Ubuntu/Alpine)

Best for a Proxmox LXC/VM or any bare-metal host. POSIX `sh`, needs only
`curl` (installed for you via `apt`/`apk` when missing):

```sh
curl -fsSL https://raw.githubusercontent.com/PPabloMunoz/coda/main/scripts/install.sh | sudo bash
```

This fetches the latest release binary, verifies `checksums.txt`, installs
to `/usr/local/bin/coda`, creates a `coda` user, and starts a service with
env-only config — a systemd unit on Debian/Ubuntu, an OpenRC service on
Alpine. No YAML editing. Then open `http://<host>:8687`.

```sh
# pin a version, port, library and data dir (roots are ':'-separated)
curl -fsSL https://raw.githubusercontent.com/PPabloMunoz/coda/main/scripts/install.sh | sudo bash -s -- \
  --version 0.1.1 --port 8687 --roots /mnt/music --data-dir /var/lib/coda
```

Flags: `--version`, `--port`, `--roots`, `--data-dir`, `--user`,
`--no-service`, `--help`. Re-run the script to upgrade.

### Docker Compose

Config via env, no YAML editing:

```sh
mkdir -p music data
sudo chown -R 1000:1000 music data
PUID=$(id -u) PGID=$(id -g) docker compose up -d
curl localhost:8687/api/v1/health
# upgrade later:
docker compose pull && docker compose up -d
```

The compose file uses `ghcr.io/ppablomunoz/coda:latest` and mounts
`./music:/music:rw` plus `./data:/data:rw` (DB, covers, and generated
`config.yaml` all live under `/data`). Ownership follows `PUID`/`PGID`
like Lidarr: coda runs as that user, so the host dirs must be owned by
the same ids or the scan fails and linking cannot move files.

### Manual binary

Download the binary for your platform plus `checksums.txt` from
[GitHub Releases](https://github.com/PPabloMunoz/coda/releases), verify it,
and put `coda` on your `PATH` (requires Go only to build from source,
developed with go1.27.3):

```sh
sha256sum -c checksums.txt
chmod +x coda-darwin-arm64
sudo mv coda-darwin-arm64 /usr/local/bin/coda
coda --version
coda --help
go run ./cmd/coda --version
```

## How to use

The UI is committed static files
under `web/static/` — no build step, no Node, no package manager.

`coda --help` prints all flags. The useful ones:

- `coda --version` — print version and exit (release builds report the
  tag, e.g. `0.1.2`; local builds report `dev`).
- `coda --check-config` — print the resolved port, roots, DB and covers
  dir, then exit without starting. Use it to validate a new setup or
  container env.
- `coda --config /path/to/config.yaml` — same as setting `CODA_CONFIG`.

Env overrides win over the config file: `CODA_CONFIG`, `CODA_DB_PATH`,
`CODA_PORT`, `CODA_ROOTS`.

```sh
go run ./cmd/coda
```

Or with [just](https://just.systems/):

```sh
just run
```

On first start Coda creates a config file if one doesn't exist:

- macOS: `~/Library/Application Support/Coda/config.yaml`
- Linux: `~/.config/coda/config.yaml`
- Override with `CODA_CONFIG=/path/to/config.yaml` or `coda --config
  /path/to/config.yaml`

Edit it to point at your library:

```yaml
port: 8687
roots:
  - /absolute/path/to/music
```

`roots` must be absolute paths to existing directories, with no duplicates
or nesting. For containers / one-off runs you can skip the file and use
env instead (env wins over the file):

```sh
CODA_PORT=8687 CODA_ROOTS=/music:/other coda --check-config
```

`CODA_ROOTS` is an OS path list: `:`-separated on macOS/Linux,
`;`-separated on Windows. Validate without starting the server:

```sh
coda --check-config
```

The SQLite database defaults to the platform data dir, not necessarily
next to the config:

- macOS: `~/Library/Application Support/Coda/coda.db` (alongside config)
- Linux: `~/.local/share/coda/coda.db` (or `$XDG_DATA_HOME/coda/coda.db`)
- Override with `CODA_DB_PATH`.

Once running, Coda scans `roots` on startup and serves the UI and API on
`http://localhost:<port>`:

```sh
curl localhost:8687/api/v1/health
curl "localhost:8687/api/v1/files?limit=20&offset=0"
curl localhost:8687/api/v1/files/1
curl localhost:8687/api/v1/files/1/suggestions
curl localhost:8687/api/v1/stats
curl localhost:8687/api/v1/files/stats
curl "localhost:8687/api/v1/artists?limit=20&offset=0"
curl "localhost:8687/api/v1/musicbrainz/artists?q=portishead"
curl "localhost:8687/api/v1/release-groups?limit=20&offset=0"
curl "localhost:8687/api/v1/releases?group=<release-group-mbid>"
```

Artists are added by MusicBrainz ID, which queues a background discography
import (see `web/README.md` for the UI pages):

```sh
curl -X POST localhost:8687/api/v1/artists \
  -H 'Content-Type: application/json' \
  -d '{"id":"<artist-mbid>"}'
curl localhost:8687/api/v1/artists/<artist-mbid>/import
curl -X POST localhost:8687/api/v1/artists/<artist-mbid>/refresh
```

Linking a file writes standard tags (title/album/artists/track/disc/date/
MBIDs) plus the release cover, and moves the file to its library path
(`<root>/<artist>/<album>/…`; occupied destinations fail with 409 and leave
the source untouched). Unlinking clears those keys only. Ranked link
candidates are available per file, so linking rarely starts from a blank
MBID:

```sh
curl -X POST localhost:8687/api/v1/files/1/link \
  -H 'Content-Type: application/json' \
  -d '{"track_id":"<track-mbid>"}'
curl -X DELETE localhost:8687/api/v1/files/1/link
```

## Contributing

Issues and pull requests are welcome. Please use the issue templates
(bug report, feature request, question) and fill in the pull request
template.

Scope is files-only: single binary + SQLite, no downloads, download
clients, or automation. Keep changes small, keep tags Navidrome-compatible,
and run the full gate before pushing:

```sh
just check
```

## License

Copyright (C) 2026 Pablo Muñoz.

Coda is free software: you can redistribute it and/or modify it under the
terms of the GNU General Public License as published by the Free Software
Foundation, either version 3 of the License, or (at your option) any later
version. See [LICENSE](LICENSE) for the full text.
