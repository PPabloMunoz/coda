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

Download the binary for your platform from
[GitHub Releases](https://github.com/PPabloMunoz/coda/releases), or build
from source (requires Go, developed with go1.27.3):

```sh
coda --version
go run ./cmd/coda --version
```

## How to use

The UI is committed static files
under `web/static/` — no build step, no Node, no package manager.

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
- Override with `CODA_CONFIG=/path/to/config.yaml`

Edit it to point at your library:

```yaml
port: 8687
roots:
  - /absolute/path/to/music
```

`roots` must be absolute paths to existing directories, with no duplicates
or nesting.

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
