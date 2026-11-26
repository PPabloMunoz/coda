# Contributing

Thanks for considering a contribution. Coda is a files-only music library
manager: single binary + SQLite, no downloads, no download clients, no
automation. Changes that fit that scope are welcome; changes that don't
will be declined, so it's worth checking an issue first.

## Reporting issues

Use the issue templates: bug report, feature request, or question. Before
filing, search existing issues and read the docs under
[`docs/`](docs/install.md). A bug report is most useful with
`coda --version`, your install method, OS, and redacted logs or curl
output.

Feature requests should state the library/metadata problem first — see
the scope check in the template. Security issues go through
[SECURITY.md](SECURITY.md), not a public issue.

## Development setup

- [Go](https://go.dev) 1.27.3 (see `go.mod`). No cgo; no other toolchain.
- [just](https://just.systems/) wraps the common commands.
- [golangci-lint](https://golangci-lint.run) for vet/lint/fmt.

| Command | What it does |
| --- | --- |
| `just run` | build, then `./bin/coda` (UI on `http://localhost:8687`) |
| `just test` | `go test ./... -cover` |
| `just vet` / `just lint` | `go vet` / golangci-lint |
| `just fmt` / `just fmt-check` | apply / check formatting |
| `just check` | the full gate: fmt-check + vet + lint + tests + build |
| `just tidy` | `go mod tidy && go mod vendor` |
| `just build-prod` | static production binary (`CGO_ENABLED=0`) |
| `just db-reset` | remove the local dev DB (macOS) |

Run `just check` before pushing — CI runs the same gate.

## Working on the code

- `main` is protected. Work on a topic branch and open a PR; a maintainer
  merges.
- PR titles follow Conventional Commits: `feat:`, `fix:`, `chore:`,
  `docs:`, `refactor:`, `test:`, `perf:`, `ci:`, `revert:`.
- Keep PRs small and focused. Fill in the PR template (the checklist
  reflects the project's invariants).
- Tests live next to the code (`go test ./...`); no fixtures are needed —
  `testdata/` is a gitignored local playground for real audio files.
- The web UI is plain HTML/CSS/JS under `web/static/`, no build step; see
  `web/README.md`. Don't add frontend tooling.
- Schema changes go in a new `000N_*.sql` migration; never edit an applied
  one. Tags written to files must stay Navidrome-compatible (standard
  frames only).
- User-facing changes need a docs update under `docs/` and a
  [CHANGELOG.md](CHANGELOG.md) entry.

For larger or cross-cutting changes, open an issue describing the problem
and smallest proposed scope before writing code.

## AI-assisted contributions

AI tools may help you write code or docs. Whatever they produce, you are
the contributor: personally review the full diff, verify the behaviour
yourself, and be able to explain and maintain the change. Declare in the
PR description whether AI was used (a simple Yes/No is enough). An agent
must not check the PR template's boxes for you.

## Releases

Maintainers only. Tag `vX.Y.Z` and CI builds the binaries
(linux/darwin, amd64/arm64), `checksums.txt`, and the `ghcr.io` image
(`X.Y.Z`, `X.Y`, `latest`). Before tagging: update `CHANGELOG.md`, and
make sure the docs match the release. Release builds report the version
via `coda --version`; local builds report `dev`.

## Documentation

- User docs: `docs/` (install, configuration, library, navidrome, api,
  operations).
- Contributor overview: [docs/architecture.md](docs/architecture.md);
  the agent-facing rulebook is [AGENTS.md](AGENTS.md).
- UI internals: `web/README.md`.

Keep docs in sync with behavior — a user-facing change without a docs
update is an incomplete change.
