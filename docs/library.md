# Library behaviour

What the scan does, how files are laid out, and exactly which tags Coda
writes. Nothing in Coda ever deletes an audio file.

## Scanning

Coda scans your roots at startup, every 6 hours, and whenever you ask:

```sh
curl -X POST localhost:8687/api/v1/scan   # 202, runs in the background
```

Force a full clear after deleting every file in a root:

```sh
curl -X POST 'localhost:8687/api/v1/scan?force_prune=true'
```

A scan:

- Walks each root and records every supported audio file. Symlinks are
  never followed; files and folders behind a symlink are skipped. Point
  roots at real directories.
- Syncs the database by absolute path. Unchanged files keep their links.
  Rows for files that vanished from disk are pruned, and their links go
  with them. A file you moved outside Coda re-appears at its new path and,
  if its tags still carry the release-track MBID, is auto-linked again.
- Fails safe on missing or unreadable roots: a root whose walk did not
  complete never prunes, and a root whose complete walk suddenly yields
  zero files keeps its rows (with a warning in the logs) instead of
  wiping links for an unmounted drive, emptied mount point, or permission
  change. Deleting individual files still prunes those rows. Pass
  `?force_prune=true` when you really deleted everything and want the
  empty root cleared.
- Tolerates unreadable files: logged, counted in the scan stats, existing
  link preserved.
- Auto-links exactly-identifiable files: a file whose tags carry a
  MusicBrainz release-track ID that resolves to a known track gets the same
  treatment as a manual link — tags rewritten, cover embedded, file moved
  to its canonical library path. Everything else is left for the link
  wizard; nothing is guessed.
- Fails safe: if the scan errors out, the API keeps serving the previous
  database state. Check the logs.

A scan never touches the network. Cover art is fetched when a client asks
for it, not during the scan. A scan gets a 10 minute budget, a discography
import 30 minutes.

## Library layout

Linked files move to a canonical path under their root:

```text
<root>/<Artist>/<Album>/NN_Title.ext
<root>/<Artist>/<Album>/DD-TT_Title.ext   # multi-disc releases
```

- The artist folder uses the first artist of the release-group credit
  (falling back to the release, then the track credit). Tags still carry
  the full credit list.
- `NN` is the zero-padded track number; `DD-TT` adds the disc number only
  when the release has more than one disc.
- Names are sanitized: `/` and `\` become `_`, the reserved characters
  `:*?"<>|` and control characters become `_`, spaces become `_`, leading
  and trailing dots/underscores are trimmed, repeated `_` collapse, and a
  segment is capped at 100 characters.
- An occupied destination refuses the move with `409` and leaves the
  source file untouched. Coda never overwrites an existing file: moves use
  no-replace semantics (hard link plus an `O_EXCL` copy fallback, so
  cross-filesystem moves work too), and link/refresh operations for the
  same file or the same destination are serialized against each other and
  the scan auto-link.

## Supported formats

aac, aif, aiff, alac, ape, flac, m4a, m4b, mp3, mpc, oga, ogg, opus, wav,
wma, wv

## Tags

Linking a file writes exactly these standard keys, plus the release cover
embedded as the front cover picture:

| Key | Content |
| --- | --- |
| `TITLE` | track title |
| `ALBUM` | album (release) title |
| `ARTIST` | artist display string |
| `ARTISTS` | per-artist credit list |
| `ALBUMARTIST` | album-artist display string |
| `ALBUMARTISTS` | per-artist album credit list |
| `TRACKNUMBER` | track number |
| `DISCNUMBER` | disc number |
| `DATE` | release date |
| `MUSICBRAINZ_TRACKID` | recording MBID |
| `MUSICBRAINZ_RELEASETRACKID` | release-track MBID (what auto-link keys on) |
| `MUSICBRAINZ_ALBUMID` | release MBID |
| `MUSICBRAINZ_RELEASEGROUPID` | release-group MBID |
| `MUSICBRAINZ_ARTISTID` | artist MBIDs |
| `MUSICBRAINZ_ALBUMARTISTID` | album-artist MBIDs |
| `RELEASETYPE` | release type (album, ep, single, …) |
| `RELEASESTATUS` | release status (official, …) |

Unlinking clears exactly those keys and the embedded cover. Every other
frame in the file is preserved.

All tag writes go through a same-directory temp copy and an atomic rename,
so a crash mid-write cannot corrupt a file. Linking runs one shared
pipeline (manual link, refresh, and scan auto-link): the file moves to its
canonical path first with no-replace semantics, then tags and cover are
written and the database link is recorded. Each run holds a per-file
operation lock across move, tag write, cover embed, and database link, plus
a per-destination lock, so concurrent links to the same file or the same
destination cannot overwrite each other. A failed move leaves tags and the
database untouched; a failed tag, cover, or database step moves the file
back without replacing anything.

## Custom covers

Hover the hero image on the artist page or the release-group page for the
refresh, upload and remove actions (icon-only; always visible on touch
screens). A small badge over the image shows where the cached art came
from ("From Cover Art Archive", "Uploaded", …). Upload replaces Coda's
cached image with a JPEG, PNG, or WebP file up to 15MB. No library rescan
is needed: the next cover GET serves the upload, with `"source": "upload"`
in the metadata row.

- Release covers propagate to audio files: the upload queues a background
  task that re-embeds the new art as the front cover picture of every file
  already linked to the release. Per-file failures only log; the upload
  itself already succeeded.
- Removing a cover deletes the cached file and keeps it gone: later cover
  requests stay empty instead of fetching provider art again. Removing a
  release cover also queues a background task that clears the embedded
  front cover from its linked files.
- Artist photos are display-only and are never written into audio files.
- Fetching provider art again ("Refresh cover"/"Refresh photo") replaces an
  upload or a removal.

## What Coda never does

- Never deletes an audio file. Unlinking and deleting artists only clear
  database rows and tags.
- Never overwrites a file at a destination path (`409` instead).
- Never guesses a link: only exact MBID matches auto-link; ambiguous files
  wait in the link wizard.

## Root and artist lifecycle

- Removing a root from the config hides its files; rows and links are kept
  and come back when the root is re-added.
- Deleting an artist removes its release groups from the database, unlinks
  its files, and leaves the audio files and their tags alone.
- Background jobs: a daily prune of collaboration-only artists left
  trackless, and a weekly purge of soft-deleted artists with no remaining
  credits. See [operations.md](operations.md) for what runs when.
