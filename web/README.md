# Coda web

The Coda UI is plain HTML/CSS/JS in `web/static/` — no build step, no
dependencies. The Go binary embeds it (`web/embed.go`, `go:embed all:static`)
and serves it; in development the same server handles `/api` directly, so
there is nothing to proxy.

## Commands

```sh
go run ./cmd/coda   # backend + UI on http://localhost:8687
```

Edit files under `web/static/` and reload the browser. Keep the UI 1:1 with
the previous design; don't add frontend infrastructure without asking.

## Pages

- `/` — artist grid and add-artist dialog
- `/artist?id=<mbid>` — artist, release groups, and saved release status
- `/artists` — legacy redirect to `/`
- `/release-group?id=<mbid>` — saved release for a group
- `/link` — unlinked files and the artist → group → track wizard
- `/files`, `/file?id=<id>` — scanned files and file details

Shared shell behaviour (health dot, rail counts, rescan, topbar search)
lives in `js/layout.js` and is loaded by every page. Page logic lives in
`js/page-*.js`; API helpers in `js/api.js`; DOM/art helpers in `js/ui.js`;
card builders in `js/cards.js`; formatting in `js/format.js`.

Cover artwork is served by the API (`GET /api/v1/.../cover`, fetched and
cached); when an item has no cached art, the browser falls back to a
deterministic placeholder sleeve generated from the name.
