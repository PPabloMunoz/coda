# Changelog

All notable changes to Coda are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org). Coda is `0.x`: breaking
changes are expected between releases, so read the **Breaking** notes
before upgrading and back up your database (see
[docs/operations.md](docs/operations.md#backup-and-restore)).

## [Unreleased]

### Added

- Cover and artist-photo images are normalized on save: every provider
  image, URL import, and file upload is downscaled to 1200px on the longest
  side (never upscaled), flattened onto white, and stored as JPEG quality
  85. The cache holds one small JPEG per release/artist instead of the
  multi-MB provider original, and the same compact bytes are embedded as
  the front cover of linked files. A `GET` that lazily fetches provider art
  for an already-linked release now queues the background re-embed, so the
  new cover reaches the audio files without a manual refresh.

### Changed

- Cover and artist-photo responses stream the cached file from disk
  (`http.ServeContent`) instead of loading it into memory, with
  `Accept-Ranges: bytes` and `206 Partial Content` support for `Range`
  requests.
- Cover and artist-photo failure logs now include the real cause chain
  instead of only the client-safe message. Failed provider chains log
  every provider attempted and its outcome, HTTP error lines name the
  release/artist from the request path, stale-file recovery records what
  was dropped, and explicit refreshes/uploads/deletes log completion.
  Normal `404` cover misses now log only at debug level.

## [0.3.1] - 2026-11-28

### Added

- Cover and artist-photo uploads accept an image URL as well as a file:
  `POST /releases/:id/cover/url` and `POST /artists/:id/cover/url` take
  `{"url": "https://…"}` and fetch the image server-side (direct `http(s)`
  link, JPEG/PNG/WebP, max 15MB; the fetched link is recorded as
  `source_url`). The upload button on the artist and release-group pages
  now opens a dialog with both choices: choose a file, or paste a URL.

## [0.3.0] - 2026-11-27

### Breaking

- Requests whose `Host` header is not allowlisted now answer `421
  INVALID_HOST`: configure `allowed_hosts` (`CODA_ALLOWED_HOSTS`) when
  serving behind a reverse proxy, and see `operations.md`
  (421 troubleshooting).
- `POST /artists` for an artist already in the library now returns `200`
  with the existing artist instead of `304`, which clients treat as a
  cache response.

### Security

- HTTP hardening: `ReadTimeout`/`WriteTimeout` 90s, `IdleTimeout` 120s,
  `MaxHeaderBytes` 64 KiB, JSON API bodies capped at 1 MiB (`413
  PAYLOAD_TOO_LARGE`; cover uploads keep their 15 MiB limit), and
  `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` plus a
  same-origin `Content-Security-Policy` on every response. The Host header
  is allowlisted (localhost, IP literals, configured `host`, and new
  `allowed_hosts` / `CODA_ALLOWED_HOSTS`); anything else answers `421
  INVALID_HOST`. See `configuration.md` (host allowlist),
  `api.md` (limits, headers), and `operations.md` (421 troubleshooting).

### Fixed

- Tag rewrites keep the original file owner: the atomic temp-copy write
  now copies the source uid/gid onto the replacement (Unix; Windows keeps
  the process owner) alongside the permission bits, so linking, unlinking,
  and cover updates no longer silently hand files to the Coda user when it
  runs as root or as a different user than the library owner. The
  copy-fallback library move (cross-device or no-hard-link filesystems;
  same-filesystem moves keep the inode and were never affected) preserves
  ownership the same way. A chown the process is not permitted to make
  only warns and the write or move still commits.
- Library moves are non-overwriting and serialized: manual link, refresh,
  and scan auto-link share one pipeline that moves with no-replace
  semantics (hard link plus an `O_EXCL` copy fallback for filesystems
  without hard links and cross-device moves) before writing tags, holds a
  per-file lock across move, tag write, cover embed, and database link plus
  a per-destination lock, and rolls back without replacing anything. An
  occupied destination answers `409` with the source untouched; concurrent
  links to the same destination resolve to one success and one `409` with
  no lost file.
- Scan pruning no longer wipes links when a root is unreadable or suddenly
  empty: the walk reports per-root completeness, incomplete roots never
  prune, and empty-but-previously-populated roots keep their rows unless
  the scan is forced (`POST /api/v1/scan?force_prune=true`).
- Switching the saved release no longer drops all file links or fails later
  imports: discography refreshes keep the user-selected release, tracklists
  reconcile (removed tracks unlink, moved tracks keep links, duplicate
  positions skip) instead of aborting, and files whose recording plus
  disc/position match the new release stay linked with tags, cover, and
  paths rewritten by a background task.
- Cover refreshes are non-destructive and re-embed: fetching provider art
  again swaps the cache only on success (a failure or no-art miss keeps the
  existing upload or cached art), a successful release-cover refresh queues
  the same background re-embed into linked files that uploads use, and
  upload/delete/refresh serialize per release or artist so concurrent
  operations can't overwrite each other. Artist-photo refreshes follow the
  same fetch-before-swap rule.
- Link actions return the real error code instead of masking missing
  entities as 500: link, unlink, refresh, and suggestions on a missing
  file, an unknown track, or a file under a removed root answer `404
  NOT_FOUND`, while an occupied destination still answers `409`.
- Artist and release-group deletes are durable: `DELETE /artists/:id` and
  `DELETE /release-groups/:id` accept `?permanent=true`, which tombstones
  the MBID in a new `ignored` table so later discography imports skip it.
  Artist deletes keep groups shared with a surviving library artist so
  they still show in that artist's "appears in". Permanently deleted
  artists block `POST /artists` with `403` until allowed again, ignored
  collaborators never reactivate, and deleting an artist during an
  in-flight import cancels the import instead of resurrecting anything.
  Allowing a permanently deleted artist again reactivates a surviving
  soft-deleted row as a collaborator, so its page offers Add to library
  instead of 404ing when it is still credited on a shared release;
  membership still requires an explicit re-add.
- Scans ignore hidden files and directories (names starting with `.`),
  so a leftover `.coda-tag-*` tag-write temp copy from a crash can never
  be indexed or auto-linked; each scan also deletes `.coda-tag-*` files
  older than 24 hours (fresh ones are left alone for in-flight writes).
- Path and timestamp edge cases: path segments are capped at 100 bytes
  (UTF-8 safe) instead of 100 runes and Windows reserved device names
  (`CON`, `NUL`, `COM1`, …) are mangled, so multi-byte titles cannot hit
  `ENAMETOOLONG` and names are creatable on Windows; same-file destinations
  (e.g. a case-only difference on a case-insensitive filesystem) link in
  place instead of wrongly answering `409`; stored and API timestamps are
  RFC3339 UTC (migration `0011` normalizes legacy rows) so they parse in
  every browser; and the installer never creates or chowns `--roots`
  directories — they must already exist.
- Background work survives restarts: discography import lifecycles
  (queued/running/done/failed) and pending cover-embed, cover-clear, and
  release-relink jobs persist in the database and are re-enqueued at
  startup, so an interrupted import resumes instead of silently resetting
  to idle and a promised cover embed still reaches linked files. Shutdown
  waits for detached cover prefetch work before the database is closed.

### Changed

- Scans are incremental and batched: `local_files` stores tag evidence
  (`mb_track_id` + `tags_read_at`, migration `0010`) so a settled rescan
  skips tag parses (same size+mtime with evidence), failed reads retry
  next time, upserts are batched multi-row statements in streamed 500-file
  chunks, and the auto-link pass pages DB candidates instead of re-reading
  every file's tags. Newly imported tracks still auto-link files with
  unresolved MBIDs. The scan bound is configurable (`scan_timeout`,
  default `10m`, `CODA_SCAN_TIMEOUT` override) and per-phase timings
  (`walk_ms`, `sync_ms`, `tag_read_ms`, `autolink_ms`) plus `tag_reads` /
  `unchanged` counters are logged and exposed in `GET /scan/status`.
  See `configuration.md`, `api.md`, `architecture.md`, and `library.md`.
- Cover image responses use `Cache-Control: no-cache` with ETag
  revalidation instead of a 24h max-age, so uploads, refreshes, and
  removals show up without a hard reload; `fetched_at` carries
  millisecond precision so rapid updates never share an ETag.
- `POST /artists` for an artist already in the library now returns `200`
  with the existing artist (and queues a refresh) instead of `304`, which
  clients treat as a cache response; `201` is for newly created artists and
  `200` for promoted collaborators. `GET /files` keeps `mtime_ns` as a raw
  Unix-nanosecond integer for change detection — all other timestamps are
  documented as RFC3339 UTC. See `api.md`.
- The install script keeps the previous port, roots, data dir and service
  user when re-run; a passed flag overrides them.
- The outbound User-Agent contact is now the project URL instead of a
  personal email.

### Added

- Cover-art miss negative cache with backoff: when no provider has art for
  a release or artist, the miss is recorded (`release_cover_misses` /
  `artist_cover_misses`) and later cover `GET`s stay `404` without provider
  calls until the retry time (6h after the first miss, doubling up to 7
  days). Removal tombstones stay separate and always win, uploads and
  successful fetches clear the miss, explicit `POST …/cover/refresh`
  always fetches immediately, and prefetch skips known gaps. Provider work
  is bounded process-wide (8 concurrent cover fetches, 20s total deadline
  per fetch) and cache hits, miss hits, and provider fetches log at debug
  level.
- Cover and artist-photo uploads (`PUT /releases/:id/cover`,
  `PUT /artists/:id/cover`, plus hover-overlay "Upload cover"/"Upload
  photo" actions on the hero image in the web UI). Release uploads replace
  the cached art immediately and queue a background task that re-embeds it
  into the release's linked files.
- Cover and artist-photo removal (`DELETE /releases/:id/cover`,
  `DELETE /artists/:id/cover`, plus a hover-overlay remove action). The
  cached file is deleted and stays deleted until the next upload or
  refresh; removing a release cover also queues a background task that
  clears the embedded art from its linked files.
- Hero source badge: the web UI shows where cached art came from ("From
  Cover Art Archive", "Uploaded", …) via new `GET …/cover/meta` endpoints
  that read the cached metadata without fetching.
- Permanently deleted list (`GET /ignored`, `DELETE /ignored/:id`, plus a
  Permanently deleted navbar tab). Delete dialogs offer an unchecked
  Permanently delete checkbox; permanent deletes tombstone the MBID so
  refreshes and imports skip it until allowed again from the new page.
- Configurable listen address: `host` in `config.yaml` (or `CODA_HOST`),
  resolved with the port into the HTTP listen address (`coda
  --check-config` shows `host`/`listen`). Docker Compose now publishes on
  loopback by default via `CODA_BIND_ADDR` (`127.0.0.1`; set `0.0.0.0` to
  expose to a trusted LAN).
- User and contributor documentation under `docs/` (install,
  configuration, library, navidrome, api, operations, architecture), a
  CONTRIBUTING guide, a security policy, a code of conduct, and this
  changelog.
- Scan status and last-run diagnostics (`GET /api/v1/scan/status`):
  the scan lifecycle (`idle`/`queued`/`running`/`done`/`failed`) with
  timestamps, duration, sync/auto-link counters, and the last failure
  plus the first 50 failed paths. The last completed run persists, so it
  survives a restart; the Rescan button polls the endpoint and reports
  queued/running/done/failed instead of a one-shot toast.
- Link unlinked files from release-group track rows: each unlinked track
  row offers a file picker so the link happens in context, and linked
  tracks read as links to their file.
- Linked-track panel on the file page: linked files show their track
  context with a link back to the release group.
- Operational guardrails: single-instance lock (`coda.db.lock` next to the
  database — a second process on the same data dir exits immediately
  instead of scanning, moving, and tagging concurrently); automatic
  pre-migration backup (`coda.db.bak-<schema-version>` via `VACUUM INTO`
  before pending migrations, one per version, never overwritten — a failed
  snapshot aborts startup); cover cache GC on every scan and after the
  weekly inactive-artist sweep (orphaned, tombstoned, and stale-MIME files
  go away); and OpenRC log rotation (`/etc/logrotate.d/coda`, weekly,
  4 kept) from the installer. `coda --check-config` also prints the lock
  path. See `operations.md` and `architecture.md`.

## [0.2.0] - 2026-11-26

### Added

- POSIX install script for Linux (systemd and OpenRC; Debian/Ubuntu,
  Fedora/RHEL, openSUSE, Arch, Alpine, Void), installing the release
  binary as a verified, hardened service.
- Docker image hardening: non-root by default, read-only root
  filesystem, dropped capabilities, image healthcheck.

### Fixed

- Atomic tag writes: same-directory temp copy, fsync and rename, so a
  crash mid-write cannot corrupt an audio file; per-path mutexes
  serialize scan-time vs handler-time writes.
- Resilient scan sync: tags are read before the write transaction,
  unchanged files are skipped, unreadable files are logged instead of
  failing the scan, and link state survives re-scans.
- Error handling hardening across the store and server layers.

## [0.1.2] - 2026-11-25

### Fixed

- Docker image metadata outputs for the ghcr.io push.

## [0.1.1] - 2026-11-25

### Added

- Docker packaging (image, compose file), env-based configuration
  (`CODA_PORT`, `CODA_ROOTS`, `CODA_CONFIG`, `CODA_DB_PATH`), and
  automated release builds (`--version` flag, checksums, ghcr.io image).

## [0.1.0] - 2026-11-25

### Added

- First public release: library scan into SQLite, MusicBrainz-backed
  artist imports and discography caching, file linking with standard tag
  writes, cover art and artist photos, embedded web UI, background task
  queue and scheduler, release automation.

[Unreleased]: https://github.com/PPabloMunoz/coda/compare/v0.3.1...HEAD
[0.3.1]: https://github.com/PPabloMunoz/coda/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/PPabloMunoz/coda/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/PPabloMunoz/coda/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/PPabloMunoz/coda/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/PPabloMunoz/coda/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/PPabloMunoz/coda/releases/tag/v0.1.0
