# Coda API

HTTP reference for Coda's API. It backs the embedded UI and is stable enough
for scripts. All routes live under `/api/v1` on the configured port (default
`8687`). Requests and responses are JSON unless noted. See [README.md]
(../README.md) for setup, [library.md](library.md) for what linking writes to
your files, [navidrome.md](navidrome.md) for player integration, and
[operations.md](operations.md) for runtime concerns.

## Conventions

### List envelope

List endpoints return a single object with a total and an item array:

```json
{"total": 42, "items": [ ... ]}
```

`total` is the full filtered count, not the page size. Entity GETs return the
object directly, not wrapped.

### Pagination

| Param    | Range | Default | Notes                             |
|----------|-------|---------|-----------------------------------|
| `limit`  | 1–100 | 20      | Non-integer or out of range → 400 |
| `offset` | ≥ 0   | 0       | Negative or non-integer → 400     |

Sort params are `sort` and `order`. Allowed `sort` keys are listed per
endpoint; an unknown key falls back to the endpoint's default. `order` is
`asc` (default) or `desc` (case-insensitive).

### Errors

Every handled failure returns JSON:

```json
{"error": "human-readable message", "code": "NOT_FOUND"}
```

| Code                | HTTP | Meaning                                  |
|---------------------|------|------------------------------------------|
| `NOT_FOUND`         | 404  | Not present, or hidden (see below)       |
| `UNAUTHORIZED`      | 401  | Not authenticated                        |
| `FORBIDDEN`         | 403  | Not allowed                              |
| `ALREADY_EXISTS`    | 409  | Conflict (occupied path, already linked) |
| `INVALID_INPUT`     | 400  | Bad query param or body                  |
| `INVALID_ID`        | 400  | Malformed MBID or file id                |
| `PAYLOAD_TOO_LARGE` | 413  | JSON body over the 1 MiB API limit       |
| `INVALID_HOST`      | 421  | Host header not in the allowlist         |
| `MUSICBRAINZ_FAILED`| 502  | MusicBrainz or artwork upstream failed   |
| *(anything else)*   | 500  | An unexpected error occurred (no code)   |

A truly unexpected error has no `code` field. 404 means "not present or
hidden": a file under a root removed from the config and a collaboration-only
artist both answer 404. The UI treats 404 as `null`.

### Request limits and security headers

JSON API bodies are capped at 1 MiB: a declared `Content-Length` over the
limit is rejected before reading, and chunked bodies are cut off while
reading. Oversized bodies answer `413 PAYLOAD_TOO_LARGE` without buffering
the whole body. Cover uploads (`PUT /releases/:id/cover`,
`PUT /artists/:id/cover`) are exempt and keep their 15 MiB limit — an
oversized cover answers `400 INVALID_INPUT`, like other cover validation
failures.

Requests whose `Host` header is not localhost, an IP literal, the
configured listen `host`, or a name in `allowed_hosts` answer `421
INVALID_HOST` (see [configuration.md](configuration.md#host-allowlist)).
The port is ignored: `coda.lan:8687` matches `coda.lan`.

Every response carries `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer`, and a same-origin `Content-Security-Policy`
(`script-src 'self'`, `style-src 'self'`, `img-src 'self' data:`,
`connect-src 'self'`). The UI ships no inline scripts or styles, so the
policy needs no `'unsafe-inline'` exception.

### MusicBrainz IDs

All `:id` path params for artists, release groups, and releases, plus
`track_id`, `artist`, and `group` values, must be a 36-character lowercase
UUID. Anything else is `INVALID_ID` (400). Local file ids are positive
integers; other values are `INVALID_ID` (400).

## Health & scan

| Method | Path           | Purpose                        |
|--------|----------------|--------------------------------|
| GET    | `/health`      | Liveness                       |
| POST   | `/scan`        | Enqueue a library rescan       |
| GET    | `/scan/status` | Scan lifecycle and last run    |

`GET /health` returns `200` with the plain text `ok` (not JSON).

`POST /scan` always returns `202` with an empty body; the scan runs
asynchronously. A pending scan is not enqueued twice (a force request
upgrades the pending scan), but a new scan queues behind one that is
already running. `?force_prune=true` allows pruning a root whose complete
walk yields zero files; without it the empty root keeps its rows, and
incomplete walks never prune even with force. An invalid `force_prune`
value is `400 INVALID_INPUT`.

`GET /scan/status` returns `200` with the scan lifecycle:

```json
{
  "state": "done",
  "queued_at": "2026-11-27T12:00:00Z",
  "started_at": "2026-11-27T12:00:01Z",
  "finished_at": "2026-11-27T12:00:05Z",
  "duration_ms": 4000,
  "found": 120,
  "upserted": 118,
  "pruned": 2,
  "autolinked": 30,
  "moved": 12,
  "skipped_conflict": 0,
  "failed_count": 1,
  "failed_files": ["/music/broken.mp3"],
  "kept_roots": [],
  "incomplete_roots": []
}
```

`state` is `idle` (no scan queued yet), `queued`, `running`, `done`, or
`failed` (with an `error` message). Timestamps describe the current
lifecycle; counters come from the last completed run's sync pass
(`found`, `upserted`, `pruned`) and auto-link pass (`autolinked`,
`moved`, `skipped_conflict`) and stay visible while a new run is
queued/running until it finishes. `failed_files` keeps the first 50
failed paths while `failed_count` is the full count. `kept_roots` lists
roots whose prune was skipped by a fail-safe guard, `incomplete_roots`
lists roots whose walk did not complete. Only `done`/`failed` runs
persist, so the last completed run survives a restart; `queued`/`running`
are memory-only.

```sh
curl -X POST http://localhost:8687/api/v1/scan
curl -X POST 'http://localhost:8687/api/v1/scan?force_prune=true'
```

## Stats

| Method | Path          | Purpose                                    |
|--------|---------------|--------------------------------------------|
| GET    | `/stats`      | Library-wide counts                        |
| GET    | `/files/stats`| File totals and per-format counts          |

`GET /stats`:

```json
{
  "artists": 10,
  "release_groups": 120,
  "releases": 135,
  "tracks": 1400,
  "ignored": 2,
  "files": {"total": 1300, "linked": 1200, "unlinked": 100,
            "formats": {"mp3": 900, "flac": 400}}
}
```

`GET /files/stats` returns the `files` object above on its own. Counts only
include files under currently configured roots.

## Files

| Method | Path                     | Purpose                               |
|--------|--------------------------|---------------------------------------|
| GET    | `/files`                 | List scanned files                    |
| GET    | `/files/stats`           | File stats (above)                    |
| GET    | `/files/:id`             | One file                              |
| GET    | `/files/:id/suggestions` | Ranked link candidates                |
| POST   | `/files/:id/link`        | Link to a track (writes tags, moves)  |
| DELETE | `/files/:id/link`        | Unlink (clears tags, keeps file)      |
| POST   | `/files/:id/refresh`     | Re-apply tags/cover/move for the link |

### `GET /files`

| Param     | Notes                                                      |
|-----------|------------------------------------------------------------|
| `limit`, `offset`, `order` | Standard pagination (above)               |
| `q`       | Substring match on path (diacritics ignored)               |
| `sort`    | `path` (default), `size`, `scanned_at`, `format`           |
| `linked`  | `true` / `false` filter on link state                      |
| `unlink`  | Any non-empty value filters to unlinked files              |
| `format`  | Case-insensitive extension match (`mp3`, `flac`, ...)      |
| `include` | `track` embeds the linked track context                    |

File objects:

```json
{
  "id": 17,
  "root_id": 1,
  "path": "/music/Artist/Album/01-03_Title.mp3",
  "size_bytes": 8123456,
  "mtime_ns": 1732000000000000000,
  "format_hint": "mp3",
  "scanned_at": "2026-01-01T10:00:00Z",
  "track_id": "b10b5e0a-...",
  "track": { "id": "b10b5e0a-...", "title": "...", "release": {...},
             "group": {...}, "credit": [...], "recording_id": "..." }
}
```

`track_id` is `null` while unlinked; `track` appears only with
`include=track`. A missing id or a file under a removed root is 404.

### `GET /files/:id/suggestions`

Returns `{"detected": ..., "suggestions": [...]}` for an unlinked file.
`detected` echoes the file's tags (`title`, `album`, `artists`, `track`,
`disc`, `filename_guess` — true when blanks were filled from the path).
Each suggestion carries `artist`, `group`, `release`, `track` context plus
`score` (0–1), `reasons`, and `has_file`.

| Param    | Notes                                       |
|----------|---------------------------------------------|
| `limit`  | 1–10, default 5                             |
| `artist` | Optional artist MBID scoping the candidates |

An already-linked file is 409 `ALREADY_EXISTS`; a missing file is 404. An
unreadable file yields empty suggestions, not an error. Read-only: never
writes tags or moves files.

### `POST /files/:id/link`

```json
{"track_id": "<musicbrainz release-track MBID>"}
```

`track_id` is the release-track MBID (`tracks.id`), not the recording MBID.
On success (`204`, empty body) Coda writes standard tags, embeds the cached
release cover, and moves the file to `<root>/<Artist>/<Album>/NN_Title.ext`.
A missing file, an unknown `track_id`, or a file under a removed root
answers `404 NOT_FOUND`. If the destination is occupied the call fails `409`
and the source file is untouched. See `library.md` for the exact tag set.

### `DELETE /files/:id/link`

`204` on success. Clears the link-written tags and the embedded cover and
removes the DB link; the file stays where it is. A missing file or a file
under a removed root answers `404 NOT_FOUND`. `POST
/files/:id/refresh` re-applies tags, cover, and the move for the file's
current link (`204`; `404` for a missing file or a file under a removed
root, `400` if the file is not linked).

## Artists

| Method | Path                         | Purpose                            |
|--------|------------------------------|------------------------------------|
| GET    | `/artists`                   | List library artists               |
| GET    | `/artists/:id`               | One artist                         |
| POST   | `/artists`                   | Add an artist by MusicBrainz MBID  |
| DELETE | `/artists/:id`               | Remove an artist                   |
| GET    | `/artists/:id/import`        | Discography import status          |
| POST   | `/artists/:id/refresh`       | Requeue discography import         |
| GET    | `/artists/:id/cover`         | Cached artist photo (image)        |
| POST   | `/artists/:id/cover/refresh` | Re-fetch the photo                 |

### `GET /artists`

| Param     | Notes                                            |
|-----------|--------------------------------------------------|
| `limit`, `offset`, `order` | Standard pagination (above)      |
| `q`       | Matches name, sort name, disambiguation          |
| `sort`    | `name` (default), `sort_name`                    |
| `all`     | `true` includes collaboration-only artists       |
| `include` | `track_stats` adds per-artist file counts        |

```json
{"id": "83d91898-...", "name": "...", "sort-name": "...",
 "disambiguation": null, "active": 1, "in_library": 1,
 "stats": {"track_count": 140, "linked_track_count": 120}}
```

`stats` appears only with `include=track_stats`. Collaboration-only artists
(`in_library: 0`) are hidden from single-artist GETs unless `?all=true`.

### `POST /artists`

```json
{"id": "<musicbrainz artist MBID>"}
```

| Status | Meaning                                                     |
|--------|-------------------------------------------------------------|
| 201    | Created; body is the artist; discography import is queued   |
| 200    | Existing collaborator promoted to the library; import queued|
| 304    | Already in the library; a refresh is requeued anyway        |
| 403    | Artist was permanently deleted; allow it again first        |

The MBID is required and must be valid (`400` otherwise). A permanently
deleted artist answers `403 FORBIDDEN` until its tombstone is removed
from `/ignored`.

### Import status and refresh

`GET /artists/:id/import` returns:

```json
{"artist_id": "83d91898-...", "state": "running",
 "queued_at": "...", "started_at": "...", "finished_at": null,
 "error": ""}
```

`state` is `idle`, `queued`, `running`, `done`, or `failed`; timestamps and
`error` are omitted when not set. `POST /artists/:id/refresh` returns `202`
with the same object. Both return 404 for artists not in the library.
`DELETE /artists/:id` returns `200` with `{"pruned": <collaborator count>}`.
Groups with no other library artist are deleted; groups shared with a
surviving library artist are kept so they still show in that artist's
"appears in".
`?permanent=true` also tombstones the artist so later imports and re-adds
cannot resurrect it; an invalid `permanent` value is `400`. A permanently
deleted artist also cancels an in-flight import instead of resurrecting it.

## MusicBrainz search

| Method | Path                    | Purpose                          |
|--------|-------------------------|----------------------------------|
| GET    | `/musicbrainz/artists`  | Proxy a MusicBrainz artist search|

| Param   | Notes                                              |
|---------|----------------------------------------------------|
| `q`     | Required, at least 2 characters                    |
| `limit` | 1–25, default 10                                   |

Items are `{id, name, sort-name, disambiguation, type, country, score}` where
`score` is MusicBrainz's own relevance value. `total` equals the number of
returned items. Upstream failure is `502` `MUSICBRAINZ_FAILED`; no results is
`404`. The client rate-limits to about one request per second against
MusicBrainz, so callers do not need their own throttling.

## Release groups

| Method | Path                         | Purpose                            |
|--------|------------------------------|------------------------------------|
| GET    | `/release-groups`            | List release groups                |
| GET    | `/release-groups/:id`        | One release group                  |
| GET    | `/release-groups/:id/cover`  | Cover of the group's release       |
| PUT    | `/release-groups/:id/update` | Swap the kept release              |
| DELETE | `/release-groups/:id`        | Delete the group                   |

### `GET /release-groups`

| Param         | Notes                                       |
|---------------|---------------------------------------------|
| `limit`, `offset`, `order` | Standard pagination (above)    |
| `q`           | Matches title or credited artist name       |
| `sort`        | `title` (default), `type`                   |
| `artist`      | Artist MBID filter                          |
| `type`        | `album`, `ep`, `single`, `other`            |
| `has_release` | `true` / `false`                            |
| `primary`     | `true` / `false`; requires `artist`         |
| `include`     | `release` embeds the saved release summary  |

```json
{"id": "f1a2...",
 "title": "...",
 "type": "album",
 "credit": [{"artist_id": "...", "name": "...", "credited_name": "...",
             "join_phrase": "", "sort_name": null, "disambiguation": null}],
 "release": {"id": "...", "title": "...", "date": "1977",
             "status": "Official", "track_count": 10,
             "linked_file_count": 8}}
```

`release` appears only with `include=release` and only when a release is
saved for the group. One release per group is enforced by the schema.

### `PUT /release-groups/:id/update`

```json
{"new_release_id": "<musicbrainz release MBID>"}
```

Replaces the group's kept release with another release of the same group
(fetched from MusicBrainz). Returns `200` with an empty body; the old
release's rows and cached cover are dropped. Files whose recording MBID
plus disc/position match a track on the new release stay linked (the
database remap is synchronous) and are retagged, re-covered, and moved to
their canonical paths by a background task; the rest unlink. A later
discography refresh keeps the switched release instead of overwriting it.
The new release must belong to
the group: `404` when MusicBrainz has no such release, `400` when it belongs
to a different group, `502` on MusicBrainz failure.

`DELETE /release-groups/:id` returns `200` with an empty body and cascades to
the release and its tracks. Files on disk are never touched.
`?permanent=true` also tombstones the group so later discography imports
skip it; an invalid `permanent` value is `400`.

## Ignored (permanently deleted)

| Method | Path             | Purpose                          |
|--------|------------------|----------------------------------|
| GET    | `/ignored`       | List tombstoned artists/groups    |
| DELETE | `/ignored/:id`   | Allow the entity again            |

`GET /ignored` lists permanently deleted tombstones with the standard
pagination (`limit`, `offset`, `order`):

| Param  | Notes                                              |
|--------|----------------------------------------------------|
| `q`    | Substring match on name (diacritics ignored)       |
| `type` | `artist`, `release_group`, or empty for all        |
| `sort` | `name` (default), `deleted_at`                     |

Items are `{"id": "...", "name": "...", "type": "artist",
"deleted_at": "..."}`. `DELETE /ignored/:id` returns `204`; removing an
artist tombstone also reactivates a surviving soft-deleted row as a
collaboration-only entry, so its page offers Add instead of 404ing —
membership still requires `POST /artists`. A group returns on the next
artist refresh or import. Nothing refreshes eagerly.

## Releases

| Method | Path                         | Purpose                        |
|--------|------------------------------|--------------------------------|
| GET    | `/releases?group=<rg MBID>`  | The release kept for a group   |
| GET    | `/releases/:id`              | One release by MBID            |
| GET    | `/releases/:id/cover`        | Cover image (below)            |
| POST   | `/releases/:id/cover/refresh`| Re-fetch the cover             |

`GET /releases` requires `group=<release-group MBID>` (`400` otherwise) and
returns that group's single release — the same object as `GET
/releases/:id`. A group with no saved release yet answers `404`. With
`include=files`, each track carries a `files` array of `{id, path,
format_hint, size_bytes}` for its linked local files. Release objects look
like:

```json
{"id": "ab5a...",
 "title": "...", "date": "1977", "status": "Official",
 "credit": [...],
 "group": {"id": "...", "title": "...", "type": "album", "credit": [...]},
 "tracks": [{"id": "...", "release_id": "...", "disc": 1, "position": 3,
             "number": "03", "title": "...", "credit": [...],
             "dur_ms": 234000, "recording_id": "..."}]}
```

## Covers

| Method | Path                         | Result                            |
|--------|------------------------------|-----------------------------------|
| GET    | `/releases/:id/cover`        | Release front cover image         |
| GET    | `/releases/:id/cover/meta`   | Release cover metadata (below)    |
| GET    | `/release-groups/:id/cover`  | Cover of the group's kept release |
| GET    | `/artists/:id/cover`         | Artist photo image                |
| GET    | `/artists/:id/cover/meta`    | Artist photo metadata (below)     |
| PUT    | `/releases/:id/cover`        | Upload a release cover            |
| PUT    | `/artists/:id/cover`         | Upload an artist photo            |
| DELETE | `/releases/:id/cover`        | Remove the release cover          |
| DELETE | `/artists/:id/cover`         | Remove the artist photo           |
| POST   | `/releases/:id/cover/refresh`| Re-fetch release cover            |
| POST   | `/artists/:id/cover/refresh` | Re-fetch artist photo             |

GET responses are raw image bytes with an `image/*` Content-Type, an `ETag`
of `"<size>-<fetched_at>"`, and `Cache-Control: no-cache`. The browser
stores the image but revalidates it on every use: send `If-None-Match` with
that ETag to get `304` when the image is unchanged, or `200` with the new
bytes after an upload or refresh. Misses (unknown id, no art, removed art)
also answer `no-cache` so a deleted cover never sticks in the browser. On
a cache miss the cover is fetched (release covers from Cover Art Archive,
then Deezer and iTunes; artist photos from Deezer, then Wikimedia) and
cached. When no provider yields usable art the answer is `404` and the
miss is remembered: later `GET`s stay `404` without calling providers again
until the retry time (6h after the first miss, doubling to 12h, 24h, … up to
7 days). An upstream failure returns an error instead of a cached miss, so
retries are possible. A removal tombstone always wins over a miss, and an
explicit `POST …/cover/refresh` always fetches immediately, bypassing the
miss. Provider work is bounded: at most 8 cover fetches run at once
(prefetch and lazy `GET`s share the budget) and one fetch never runs longer
than 20s in total.

`POST /releases/:id/cover/refresh` and `POST /artists/:id/cover/refresh`
fetch provider art again and swap the cache only on success, returning
`200` with the cover metadata row. A fetch failure or a no-art miss leaves
the previously cached art untouched (the upload or earlier fetch survives);
a successful refresh clears an upload or a removal tombstone:

```json
{"release_id": "ab5a...", "source": "coverartarchive",
 "source_url": "https://...", "mime": "image/jpeg",
 "size_bytes": 123456, "fetched_at": "2026-01-01T10:00:00Z"}
```

Artist refreshes use `artist_id` instead of `release_id`. Refresh endpoints
return `404` for unknown ids and `500` when cover fetching is disabled.
Artist photos are display-only; they are never embedded into audio files.

`GET …/cover/meta` returns the cached metadata row without fetching
anything (same shape as the refresh response). It answers `404` when the
entity is unknown, no art is cached, or the art was removed — the web UI
uses it for the hero "From …" source badge.

### Uploading covers

`PUT /releases/:id/cover` and `PUT /artists/:id/cover` replace the cached
image with caller-supplied bytes. The body is the raw image (no multipart),
up to 15MB; the real type is sniffed from magic bytes and must be JPEG,
PNG, or WebP, otherwise the answer is `400 INVALID_INPUT`. Unknown ids
answer `404`, like the refresh endpoints.

```sh
curl -X PUT localhost:8687/api/v1/releases/<mbid>/cover \
  --data-binary @cover.png -H 'Content-Type: image/png'
```

A successful upload returns `200` with the same metadata row shape as a
refresh, with `"source": "upload"` and `"source_url": null`. Uploads work
even when provider fetching is disabled. A later refresh discards the
upload and fetches provider art again.

Uploading a release cover also queues a background task that re-embeds the
new art into every file already linked to the release. A successful
refresh queues the same task. The upload or refresh itself succeeds first;
embedding failures only log (check the server logs), so a
file that can't be rewritten doesn't fail the upload or refresh.

### Removing covers

`DELETE /releases/:id/cover` and `DELETE /artists/:id/cover` delete the
cached image file and answer `204` with no body. Unknown ids answer `404`;
removing an already-removed or never-cached cover still answers `204`.

A removal is sticky: it records a tombstone so later `GET …/cover` calls
stay `404` instead of lazy-fetching provider art again. Only a new upload
or a refresh clears it (refresh fetches provider art again as usual).

Removing a release cover also queues a background task that clears the
embedded front cover from every file already linked to the release. Like
the embed task, the removal succeeds first and per-file failures only log.

## Async operations

`POST /scan`, `POST /artists`, and `POST /artists/:id/refresh` enqueue work
that runs in a serial background queue. Progress is only observable for
artist imports (`GET /artists/:id/import`); a scan is visible through
`/stats` and `/files` once it has synced. See `operations.md` for the
scheduler's periodic jobs.

Import statuses and pending cover-embed, cover-clear, and release-relink
jobs survive restarts: they persist in the database and are re-enqueued at
startup, so an interrupted import resumes instead of resetting to `idle`
and a promised file update still runs. Terminal states (`done`, `failed`)
stay visible too. Scans and periodic prunes are not persisted — the
startup scan and the scheduler re-enqueue them.
