# Using Coda with Navidrome

Coda's job is to make your files carry correct, standard tags so players
like [Navidrome](https://www.navidrome.org) show the right metadata. Coda
writes only standard frames — see the full key list in
[library.md](library.md#tags) — so nothing it writes is player-specific.

## Setup

- Point Navidrome's music folder at the same directory (or a parent of
  your Coda roots).
- Let Coda manage the tags; Navidrome reads them. Don't edit the same
  files with two taggers at once.

## What to expect

- **Standard frames only.** Title, album, artist(s), album-artist(s),
  track, disc, date, the MusicBrainz MBIDs, release type/status, and the
  front cover picture. No custom fields.
- **Files move.** Linking a file moves it to
  `<root>/<Artist>/<Album>/NN_Title.ext` (see
  [library.md](library.md#library-layout)). After a Coda linking session,
  trigger a rescan in Navidrome so it picks up the new paths and tags.
- **Covers are embedded.** Linked files carry the release cover as their
  front cover picture, so Navidrome shows art without sidecar files.
  Covers are cached as 1200px JPEGs, so the embedded art is a few hundred
  KB per file, not the multi-MB original.
  Uploading a new release cover in Coda re-embeds it into the release's
  linked files in the background (and removing a cover clears it), as does
  a successful cover refresh — rescan Navidrome afterwards to pick up the
  change.
- **Multi-artist credits.** `ARTIST`/`ALBUMARTIST` keep a display string;
  `ARTISTS`/`ALBUMARTISTS` carry the per-artist lists. How a player
  combines them is up to the player.
- **Placeholder art stays in Coda.** The generated sleeve you see in
  Coda's UI for files without covers is browser-side only; nothing is
  written to your library.

## If Navidrome shows something different

- Rescan the Navidrome library after linking or after a Coda scan that
  moved files.
- Check the file's tags directly (`ffprobe`, `kid3`, …) — Coda wrote them
  at link time; the player reads them on its next scan.
- Unlinked files are untouched by Coda, so they keep whatever tags they
  had before.
