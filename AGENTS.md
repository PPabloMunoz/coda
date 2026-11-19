# AGENTS.md

Coda: self-hosted music library manager (Go + SQLite) — a simpler, faster Lidarr for files only. Scope: read audio files from configured roots, write tags, and link local files to releases via tags so libraries work with Navidrome. Non-goals: downloads, download-client integration, automation. Early prototype — expect breaking changes.

## Commands

Use `just` (wrappers around `go` + `pnpm`):

- `just run` — `check` + `build`, then `./bin/coda`. `build` includes `web-build` (`pnpm install --frozen-lockfile` + `pnpm build`, requires Node >=22). Backend-only without pnpm: `go run ./cmd/coda`.
- `just check` — full gate: Go `fmt-check` + `go vet` + `golangci-lint` + `go test` + `go build ./...`, plus web `oxlint` (`web-lint`) and `oxfmt --check` (`web-fmt-check`). Run after every change, before finishing.
- `just fmt` — apply formatting: Go (`golangci-lint fmt`) + web (`oxfmt`). Run it when `just check` reports format diffs, then re-run `just check`.
- `just build` / `just build-prod` — `web-build` + `go build -o bin/coda ./cmd/coda` (`build-prod` adds `CGO_ENABLED=0` static, pure-Go SQLite, no cgo needed).
- `just web-build` / `just web-dev` — pnpm install/build the Astro UI, or run the dev server (proxies `/api` to `localhost:8687`).
- `just tidy` — `go mod tidy && go mod vendor` (`vendor/` is gitignored local output, not committed).
- `just db-reset` — macOS-only (`rm -f "$HOME/Library/Application Support/Coda/"*.db*`); on Linux remove `$XDG_DATA_HOME/coda/*.db*` (or `~/.local/share/coda/`) or the `CODA_DB_PATH` target instead.

Env overrides: `CODA_CONFIG=/path/to/config.yaml`, `CODA_DB_PATH=/path/to/coda.db`. No test files exist yet (`go test ./...` passes vacuously) — don't add test scaffolding unless asked.

## Runtime

- Entrypoint `cmd/coda/main.go`: `GetConfig` → `GetDBPath` → `NewStore` (runs goose migrations) → `AddRootDirs` (fatal on error) → `musicbrainz.New` (single shared client) → `NewQueue`/`NewRunner` → `NewHandler` → `http.Server` in goroutine (`ReadHeaderTimeout` 5s) → `runner.Start` + `EnqueueScan` → 10s graceful shutdown. Scan ctx is 10 min, discography import ctx is 30 min; scan failure only logs and keeps serving stale DB.
- The scan itself never modifies audio files; tag writes happen only via `POST /api/files/{id}/link` and `DELETE /api/files/{id}/link`.
- First start creates `config.yaml` with defaults (`port: 8687`, empty `roots`); edit `roots` to absolute existing dirs. Validation trims/cleans, rejects empty/relative/duplicate/nested roots, resolves symlinks, and `Stat`s each root is a dir.
- State location is per-OS: macOS config + DB both under `~/Library/Application Support/Coda/`; Linux config is `~/.config/coda/config.yaml` but DB is `~/.local/share/coda/coda.db` (or `$XDG_DATA_HOME/coda/`); Windows prefers `LOCALAPPDATA`/`AppData`. `bin/`, `*.db*`, and `/testdata/` are gitignored (testdata holds local real MP3s only).

## Layout (`internal/`)

- `config/` — `config.yaml` + DB path resolution (per-OS defaults, `CODA_*` overrides). DB filename constant embeds SQLite pragmas (`WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout`); `CODA_DB_PATH` is passed through with pragmas appended.
- `store/` — SQLite via `modernc.org/sqlite`, migrations embedded (`migrations/*.sql` via goose). `db.SetMaxOpenConns(1)` is required — don't raise it. New schema changes go in `0002_*.sql`; never edit `0001_init.sql`.
- `library/` — filesystem walk: skips symlinks, non-regular files, unreadable entries, and non-audio extensions (`SupportedFormats` map). Never follow symlinks.
- `tags/` (`go.senan.xyz/taglib`, `bogem/id3v2` for MP3 TXXX normalize) — `Snapshot`/`Read` is the read view; `WriteTags`/`ClearTags` implement link/unlink and touch only `LinkKeys` (title/album/artist(s)/album-artist(s)/track/disc/date/MBIDs/release-type/status). `ARTISTS` comes only from the `ARTISTS` frame, never by splitting `ARTIST`; `ArtistRaw` is display-only. Bad `TRACKNUMBER`/`DISCNUMBER` parses to 0 (missing), never an error.
- `musicbrainz/` — client enforces ~1 req/1.1s rate limit, 3 retries with backoff (`Retry-After` aware), mandatory `User-Agent`. Lowercase-hex MBID validation; `ErrNotFound` maps to 404, `ErrInvalidMBID` to 400, search failures to 502 in handlers.
- `server/` — stdlib `net/http` `ServeMux` with method patterns. Routes: `GET /api/health`, `GET /api/stats`, `GET /api/files` (+ `stats`, `/{id}`, `/{id}/link`), `GET /api/artists` (+ `/{id}`, `/{id}/import`, `POST /`, `POST /{id}/refresh`), `GET /api/musicbrainz/artists`, `GET /api/release-groups` (+ `/{id}`), `GET /api/releases` (`?group=`, `/{id}`), plus embedded UI catch-all `/`. List endpoints paginate (`limit` 1–100 default 20, `offset` ≥ 0); `GET /api/musicbrainz/artists` uses `limit` 1–25 default 10 and requires `q` ≥ 2 chars. Missing entity returns `404` with JSON `null` via `store.ErrNotFound`, not an error object. `POST /api/artists` returns `304` (`null` body) if the MBID is already stored (and requeues a refresh), else fetches MusicBrainz, `CreateArtist`, enqueues discography import, and returns `201`. Enqueue/refresh state lives in `tasks.Queue` (`artistImports` map + mutex), not a handler-local goroutine.
- `tasks/` — serial FIFO `Queue` (`run` loop) + `Runner` dispatch (`scan-roots`, `import-discography`). Dedup only for artist imports while `queued`/`running`; scans have no dedup (`TODO` in `queue.go`). Scan upserts by absolute path (preserving `track_id`) and prunes rows with `scanned_at` older than the run. Import status is `idle/queued/running/done/failed` (`GET /api/artists/{id}/import`, `POST /api/artists/{id}/refresh` → `202`).
- `utils/` — tiny stderr `Logger` (`Info`/`Error` + `f` variants, no levels to configure) plus `ValidateMBID` and `NullIfEmpty`.
- `web/` — Astro static UI (`web/src`, proxied `/api` in dev). `just web-build` outputs `web/dist`, embedded by `web/embed.go` (`go:embed all:dist`) and served catch-all by `server/web.go` after the `/api/*` routes (404 `web build missing` fallback). `web/dist/*` is gitignored build output. Don't expand UI scope or add frontend infra without asking.

## Schema / metadata rules (load-bearing)

- Entity split is deliberate: `artists / artist_credits / release_groups / releases / tracks / local_files` (one release per release-group; recording MBIDs live on `tracks`). Credits reference `artist_credits(id)` only — never add an `artist_id` shortcut or collapse to one artist string.
- The core job is linking files to music: `local_files.track_id` ties a scanned file to a release track, and tags written back to files must stay Navidrome-compatible (standard frames only — don't invent custom tag fields).
- MusicBrainz IDs are `TEXT UNIQUE` (nullable until matched); refresh by MBID, never text matching. MBID columns enforce 36-char length.
- Don't add services (Redis, queues, K8s), download clients, automation, `docs/decisions|`architecture|roadmap` files, or unrelated refactors. Keep changes small; single binary + SQLite only.
