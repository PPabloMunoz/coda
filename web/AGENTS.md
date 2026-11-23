# web

Plain HTML/CSS/JS under `web/static/`, served embedded by the Go binary.
No build step, no Node, no package manager.

- Edit `web/static/` directly and reload the browser (`go run ./cmd/coda`).
- One `.html` file per route, one `js/page-*.js` per page; shared shell
  behaviour in `js/layout.js`, shared helpers in `js/api.js`, `js/ui.js`,
  `js/cards.js`, `js/format.js`.
- `web/static/css/style.css` is the single stylesheet.
- Don't add bundlers, frameworks, or frontend tooling without asking.
