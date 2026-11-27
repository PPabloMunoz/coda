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
| `covers/` | cached release covers (`<release-mbid>.jpg|png|webp`) and artist photos (`artist-<mbid>.*`) |
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
again. Covers can be omitted from a backup; they are re-fetched lazily.

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
| OpenRC | `/var/log/coda.log` |
| Docker | `docker compose logs -f` |
| Manual | stderr; redirect to a file as you like |

## Health and diagnostics

- `GET /api/v1/health` answers `200 ok` (the Docker image health-checks
  this itself).
- `coda --check-config` prints the resolved listen address, port, roots, DB and covers
  paths and exits without starting — the first thing to run when config
  seems off.

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
provider has art, the UI shows a generated placeholder; refresh endpoints
exist per artist, release group and release (see [api.md](api.md)).

**HTTP 421 host not allowed.** The `Host` header is not localhost, an IP
literal, the configured `host`, or a name in `allowed_hosts`. Add the name
you browse by to `allowed_hosts` in `config.yaml` (or `CODA_ALLOWED_HOSTS`)
and restart; verify with `coda --check-config`. Behind a reverse proxy,
list the public hostname. Direct-IP access never needs listing.

**UI seems stale.** A failed scan keeps serving the last good data. Check
the logs, fix the cause, and trigger a rescan (`POST /api/v1/scan`).
