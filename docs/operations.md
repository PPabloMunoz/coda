# Operations

Day-to-day care of a running Coda: where state lives, backups, upgrades,
logs, and what to check when something looks wrong.

## Where Coda keeps data

| Install | State directory |
| --- | --- |
| Installer (Linux) | `/var/lib/coda` |
| Docker | the host dir mounted at `/data` (default `./data`) |
| Manual / source | see [configuration.md](configuration.md#data-locations) |

Inside it:

| Path | What |
| --- | --- |
| `coda.db` | SQLite database (WAL mode) |
| `coda.db-wal`, `coda.db-shm` | WAL sidecars; part of the database while running |
| `coda.db.lock` | single-instance lock file; a second process on the same data dir exits immediately instead of starting |
| `coda.db.bak-*` | automatic pre-migration snapshots (`bak-<schema-version>`); see below |
| `covers/` | cached release covers (`<release-mbid>.jpg`, normalized to 1200px JPEG) and artist photos (`artist-<mbid>.jpg`) |
| `config.yaml` | config (created on first start; the installer runs env-only) |

Everything is disposable except the database and covers: files are read
from your music roots, covers can be re-fetched, the config is regenerated.

## Backup and restore

Stop Coda first so the WAL is checkpointed, then copy the state directory:

```sh
sudo systemctl stop coda                      # docker compose stop
cp -a /var/lib/coda /backup/coda-$(date +%F)  # db + covers + config
sudo systemctl start coda
```

To back up a live database without stopping, use SQLite's online backup
instead of copying files:

```sh
sqlite3 /var/lib/coda/coda.db ".backup '/backup/coda.db'"
```

Restore by stopping Coda, replacing the directory contents, and starting
again. Covers can be omitted from a backup; stale ones are swept by the
cover GC on the next scan and missing ones are re-fetched lazily.

## Automatic pre-migration backup

Before applying pending schema migrations at startup, Coda snapshots the
database to `coda.db.bak-<schema-version>` next to the database (a
consistent `VACUUM INTO` copy, safe with WAL active). One snapshot per
schema version is kept and never overwritten; a failed snapshot aborts
startup so a migration never runs unprotected. Fresh databases and
up-to-date databases need no snapshot.

To roll back after a bad migration: stop Coda, replace `coda.db` with the
`coda.db.bak-*` copy, restore the previous binary version, and start
again. Old `coda.db.bak-*` files can be removed by hand once you are happy
with the new version.

## Upgrades

Database migrations run automatically at startup (goose, forward-only —
there is no downgrade path). Coda is `0.x`: breaking changes are expected,
so before upgrading:

1. Back up (above).
2. Read [CHANGELOG.md](../CHANGELOG.md) for breaking changes.
3. Upgrade: re-run the installer, `docker compose pull && up -d`, or
   replace the binary. Then check `coda --version` and the health
   endpoint.

A failed scan after an upgrade keeps serving the previous database state;
check the logs.

## Logs

Logs go to stderr as `YYYY-MM-DD HH:MM:SS level message`. Debug logs are
off by default; enable with `CODA_DEBUG=1` (or `CODA_LOG_LEVEL=debug`).

| Install | Where |
| --- | --- |
| systemd | `journalctl -u coda -f` |
| OpenRC | `/var/log/coda.log`, rotated weekly by `/etc/logrotate.d/coda` (4 kept; needs the `logrotate` package) |
| Docker | `docker compose logs -f` |
| Manual | stderr; redirect to a file as you like |

## Health and diagnostics

- `GET /api/v1/health` answers `200 ok` (the Docker image health-checks
  this itself).
- `coda --check-config` prints the resolved listen address, port, roots, scan
  timeout, DB and covers paths and exits without starting — the first
  thing to run when config seems off.
- `GET /api/v1/scan/status` reports the scan lifecycle
  (`idle`/`queued`/`running`/`done`/`failed`) with timestamps, duration,
  per-phase timings (`walk_ms`, `sync_ms`, `tag_read_ms`, `autolink_ms`),
  incremental counters (`tag_reads`, `unchanged`), sync/auto-link counters,
  and the last failure plus the first 50 failed paths. A settled rescan
  shows `tag_reads` near 0 with `unchanged` covering the library. The last
  `done`/`failed` run survives a restart. `scan_timeout` (default `10m`,
  `CODA_SCAN_TIMEOUT` override) bounds the whole scan; increase it for
  large or slow libraries.

## Troubleshooting

**Scan finds 0 files.** Check `coda --check-config`: roots empty or wrong?
Symlinks inside a root are skipped (never followed) — point roots at real
directories. Unreadable directories mark that root's walk incomplete and
skip pruning for it (see the logs); unsupported extensions are ignored
(see the list in [library.md](library.md#supported-formats)).

**Files vanished from the UI.** They were pruned: gone from disk, or
under a root you removed. Re-adding the root brings rows and links back.
A missing/unreadable root never prunes, and a root that suddenly yields
zero files keeps its rows unless you forced the prune
(`POST /api/v1/scan?force_prune=true`). Files moved outside Coda
re-appear at the new path and auto-link again if their tags still identify
the track.

**Link fails with 409.** The destination path is already occupied. Coda
never overwrites; resolve the duplicate by hand and retry.

**Permission errors on link/scan.** The service user (or `PUID:PGID` in
Docker) needs read/write on the roots — linking moves files. The installer
warns about this; fix with `chown`.

**MusicBrainz errors (502).** Upstream trouble. Coda rate-limits itself to
about one request per 1.1s and retries with backoff; try again later.

**Cover art missing.** Covers are fetched lazily from Cover Art Archive →
Deezer → iTunes (artist photos: Deezer → Wikimedia Commons). If no
provider has art, the UI shows a generated placeholder and the miss is
remembered (6h at first, doubling up to 7 days) so pages stop hammering
providers; refresh endpoints exist per artist, release group and release
(see [api.md](api.md)) and always fetch immediately, bypassing the wait.

**Cover refresh fails with "An unexpected error occurred".** The API keeps
that safe message for clients; the server log has the detail. A failed
provider chain logs one line naming the release/artist, every provider
tried, and the outcome, for example:

```text
WARN artwork: release <mbid> ("Artist" - "Album"): CAA: artwork: status 503; Deezer: no match; iTunes: no match in 2.6s
WARN cover prefetch release "<mbid>": cover fetch release="<mbid>" artist="Artist" album="Album": artwork: status 503
```

A failed explicit refresh/upload names the release/artist and says whether
the previous cached art was kept. A missing cached file is recovered
automatically (the stale row is dropped and the image refetched); normal
`404` cover misses are logged only when `CODA_DEBUG=1` is enabled.

**HTTP 421 host not allowed.** The `Host` header is not localhost, an IP
literal, the configured `host`, or a name in `allowed_hosts`. Add the name
you browse by to `allowed_hosts` in `config.yaml` (or `CODA_ALLOWED_HOSTS`)
and restart; verify with `coda --check-config`. Behind a reverse proxy,
list the public hostname. Direct-IP access never needs listing.

**"another coda instance is already running".** A second Coda process was
started on the same data dir (same `CODA_DB_PATH`). Stop the first one;
only one process may own a database. The guard is a lock file
(`coda.db.lock` next to the database) and is host-local — it does not
arbitrate network filesystems.

**UI seems stale.** A failed scan keeps serving the last good data. Check
`GET /api/v1/scan/status` first: it reports `queued`/`running` while work
is in flight and `done`/`failed` with counters, duration, and (on
failure) the error once finished. Then check the logs, fix the cause, and
trigger a rescan (`POST /api/v1/scan`).

**Restart during an import or cover update.** Queued and running imports
resume after the restart instead of resetting to idle, and pending
cover-embed, cover-clear, and release-relink jobs are re-enqueued; `done`
and `failed` import states stay visible. The last completed scan (`done`
or `failed`) also stays visible via `GET /api/v1/scan/status`; an
interrupted scan itself is not resumed — the startup scan and the
scheduler re-enqueue it, as do periodic prunes.
