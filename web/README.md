# Coda web

The Astro UI for Coda. It is a static build that talks to the Go API at
`/api`; in development Vite proxies those requests to `http://localhost:8687`.

## Commands

```sh
pnpm install
pnpm dev      # dev server, proxying /api to the Go server
pnpm build    # production build in web/dist, embedded by the Go binary
```

The backend must be running for pages to load library data (`just run` from the
repository root).

## Pages

- `/` — artist grid and add-artist dialog
- `/artist?id=<mbid>` — artist, release groups, and saved release status
- `/release-groups` — all imported release groups
- `/release-group?id=<mbid>` — saved release for a group
- `/release?id=<mbid>` — tracklist with MusicBrainz and local-file links
- `/link` — unlinked files and the artist → group → release → track wizard
- `/files`, `/file?id=<id>` — scanned files and file details

Placeholder artwork is generated in the browser from each artist or release
name (a deterministic record sleeve); no cover images are fetched.
