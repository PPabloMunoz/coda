# Pull request

## What / why

<!-- What changed and why? Link issues with `Fixes #123`. -->

## Testing

<!-- How did you verify? Paste `just check` output. -->

```sh
just check
```

## Checklist

- [ ] `just check` passes (Go fmt/vet/lint/tests/build + web lint/format).
- [ ] Change is small and files-only (no downloads, download clients, or automation).
- [ ] No schema change, or migration added as new `000N_*.sql` (never edited `0001_init.sql`).
- [ ] Tags stay Navidrome-compatible (standard frames only, `LinkKeys` only on link/unlink).
