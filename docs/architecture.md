# Architecture

A human-facing overview of how Coda fits together. For the agent-facing
rulebook, see [AGENTS.md](../AGENTS.md); for day-to-day usage, the other
pages under `docs/`.

## Shape

One Go binary, one SQLite file, one embedded web UI. No external services,
no downloads, no automation. The binary:

- reads audio files from configured roots,
- links them to MusicBrainz releases and writes tags,
- serves a small HTTP API plus the static UI.

SQLite is `modernc.org/sqlite` (pure Go, no cgo), so release builds are
fully static (`CGO_ENABLED=0`).

## Package map

| Package | Role |
| --- | --- |
| `cmd/coda` | wiring: config → store → clients → queue/runner/scheduler → HTTP server, 10s graceful shutdown |
| `internal/config` | `config.yaml` + env overrides; per-OS config/DB paths; SQLite pragmas; covers dir |
| `internal/store` | SQLite via a single connection; goose migrations; all queries; scan sync |
| `internal/errors` | the error vocabulary: `AppError{Code, Message, Cause}` and sentinels |
| `internal/library` | filesystem walk (symlink-skipping) and canonical library paths |
| `internal/tags` | tag read/write via go.senan.xyz/taglib; atomic writes; link key set |
| `internal/artwork` | cover/artist-photo fetcher with provider fallback |
| `internal/musicbrainz` | rate-limited WS/2 client (~1 req/1.1s, retries, mandatory User-Agent) |
| `internal/server` | Gin handlers, `/api/v1` routes, embedded UI serving, error→status mapping |
| `internal/tasks` | serial FIFO task queue: scans, imports, prunes; scheduler ticks |
| `internal/utils` | tiny logger, MBID validation, path sanitization |
| `web/` | vanilla HTML/CSS/JS, embedded via `go:embed` (see `web/README.md`) |

## Startup and request flow

`cmd/coda/main.go` resolves config and the DB path, opens the store (which
runs migrations), validates roots, creates the shared MusicBrainz client
and artwork fetcher, then starts the task runner, the scheduler, and the
HTTP server, and enqueues the initial scan.

Requests flow: UI or scripts → Gin (`/api/v1`) → handler → store → SQLite.
Handlers never talk to the filesystem directly; tag and file mutations go
through `internal/tags` (atomic temp-copy + rename) and `internal/library`
(path building).

## Errors

All errors are `internal/errors.AppError`. Service code wraps at the
origin with a stable `Code`; `server/errors.go` maps Code → HTTP status
(`NOT_FOUND` 404, `ALREADY_EXISTS` 409, `INVALID_ID`/`INVALID_INPUT` 400,
`MUSICBRAINZ_FAILED` 502, else 500) and returns only the safe `Message`
to clients. Codes are the API contract — never rename one.

## Background tasks

A serial FIFO queue runs every job; nothing runs concurrently with
anything else. The scheduler ticks: scan every 6h, orphan-credit prune
every 24h, delete-inactive-artists weekly. A scan gets 10 minutes, a
discography import 30. Scan failures are logged and the previous database
state keeps serving.

Import lifecycles and pending release file jobs (cover embeds/clears,
relinks) are durable: they persist in `artist_imports` and
`pending_release_tasks` and are re-enqueued at startup, so a restart
resumes an interrupted import instead of resetting its status to idle,
and a promised cover embed still reaches linked files. Scans and periodic
prunes are not persisted — the startup scan and the scheduler re-enqueue
them. Detached cover prefetch work derives from the runner context and
`Start` waits for it, so shutdown cancels it within the grace period and
the store closes only after background workers stop.

## Scan pipeline

1. `library.Scan` walks each root (symlinks never followed) and reports
   per-root walk completeness.
2. `store.SyncLocalFiles` upserts by absolute path, preserving `track_id`;
   tags are read before the write transaction; unchanged files (size +
   mtime) are skipped; rows for vanished files are pruned, except roots
   with an incomplete walk (never prune) and roots whose complete walk
   yields zero files (kept unless the scan was force-pruned).
3. The auto-link pass (`tasks/autolink.go`) links every file whose on-disk
   release-track MBID resolves to a known track through the same shared
   no-replace link pipeline as manual link: move first, then tags, cover,
   and database link. Occupied destinations are skipped and reported, never
   overwritten.
4. Orphan credits and trackless collaborators are cleaned up.

## Data model

`artists / artist_credits / artist_credit_items / release_groups /
releases / tracks / local_files / roots`, plus cover tables, the
`ignored` tombstone table, and the background-task intent tables
(`artist_imports`, `pending_release_tasks`). The split is deliberate: credits are normalized
(`artist_credits`), recording MBIDs live on `tracks`, and
`local_files.track_id` is the single link between the filesystem and music
metadata. One saved release per release-group. `ignored` holds permanently
deleted artist and release-group MBIDs so imports skip them until removed.

## Conventions worth knowing

- MBIDs are validated everywhere; lookups and refreshes go by MBID, never
  by text matching.
- `db.SetMaxOpenConns(1)` is load-bearing: SQLite through one connection.
- Tag writes only touch the link key set; everything else in the file is
  preserved.
- `vendor/` is gitignored local output (`just tidy`); the UI has no build
  step and is committed static files.
