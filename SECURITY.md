# Security Policy

## Reporting a vulnerability

Report vulnerabilities privately through GitHub's security advisory
feature ("Report a vulnerability" on the Security tab of this
repository). Please do not open a public issue for a security report.

Include what you found, how to reproduce it, the affected version
(`coda --version`), and how you run Coda (installer / Docker / manual /
source). You can expect an initial response within a few days; fixes land
in the next release.

## Supported versions

Only the latest `0.x` release receives security fixes. Coda is an early
prototype with breaking changes between releases — upgrade before
reporting, and read the [CHANGELOG](CHANGELOG.md).

## Deployment caveats

Coda has **no authentication** by design and serves its full API and UI to
anyone who can reach the port. The binary listens on all interfaces by
default; set `host: 127.0.0.1` in `config.yaml` (or `CODA_HOST=127.0.0.1`)
to restrict it to loopback. Docker Compose publishes on loopback by
default — keep `CODA_BIND_ADDR=127.0.0.1` (or put Coda behind an
authenticating reverse proxy) unless your LAN is trusted. Reports that assume public internet exposure without
authentication should propose a design that fits Coda's scope (a single
binary, no external services).

Coda also makes outbound requests to MusicBrainz, the Cover Art Archive,
Deezer, iTunes and Wikimedia Commons, and moves/renames files inside the
configured roots. The Docker image ships a hardened, read-only, non-root
container by default.
