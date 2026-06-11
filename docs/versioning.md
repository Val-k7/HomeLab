# Versioning & stability policy

The platform follows a documented versioning contract on its road to a stable
1.0. This page is that commitment: how releases are numbered today, and what
becomes binding once 1.0 ships.

> **Type:** reference · **Audience:** operator · **Last reviewed:** 2026-06-11

## Semantic versioning

Releases follow `MAJOR.MINOR.PATCH`.

- **MAJOR** — a breaking change to a stable surface (see *Stable surface*).
- **MINOR** — backwards-compatible features.
- **PATCH** — backwards-compatible fixes.

Pre-1.0, breaking changes may still occur between minors as the surface
settles. From **1.0** onward, the rules below bind.

## Stable surface (frozen at v1.0)

These are the contracts a major bump is required to break:

1. **`control-api` `/v1/*`** — request/response shape and auth/role semantics
   (see [api.md](api.md)). New endpoints/fields are additive (minor); removing or
   repurposing one is major.
2. **NixOS module options** — `config/platform.nix`, `config/policies.nix`,
   `config/catalogs.nix` schemas and `modules/*.nix` option names/defaults.
3. **App manifest schema** — `apps/*.nix` (schemaVersion 2) and the normalized
   model in `lib/app-model.nix`.
4. **On-disk manifests** — `/etc/homelab/{platform,policies,catalogs,apps}.json`
   consumed by control-api and `cmd/validate-platform`.

Not part of the stable surface: internal Go package layout, the web UI,
`bin/*` script internals, and the MCP debug tool.

## Deprecation policy

A stable element is deprecated for **at least one minor release** — announced in
the changelog and, where possible, kept working with a warning — before removal
in the next major.

## Release process

1. Land changes on `main` (gated by `.github/workflows/checks.yml`).
2. Add `docs/changelog/<version>.md`.
3. Tag `vX.Y[.Z]`. The `release.yml` workflow publishes the GitHub Release from
   the changelog automatically.
4. The sanitized public mirror is published by `release.sh` (maintainer-run).

## Road to 1.0

1.0 is cut when every gate is green and the surface above is documented and
stable:

- [x] Strict policy enforced and gated in CI.
- [x] Backups prune + restore-test; durable-data policy.
- [x] Catalog schema validated as a flake check.
- [x] CI gates `main` (not just PRs): vet, test, staticcheck, gofmt, gitleaks,
      actionlint, web build, fleet `nix flake check`, strict `validate-platform`.
- [x] `/v1` reference + this policy documented.
- [x] End-to-end restore test implemented as `tests/restore-e2e.nix`, gated in
      `checks.yml` and `release.yml`.
- [ ] `govulncheck` gating once the Go toolchain ships the stdlib fixes.

When the remaining boxes are checked, the next tag is `v1.0.0`.
