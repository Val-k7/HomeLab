# Test suite reference

What each test suite covers and how to run it: Go unit tests in [control-api/](../control-api/), eval-time Nix tests in [tests/](../tests/), and the restore end-to-end VM test. CI wiring is described in [reference-ci-pipelines.md](reference-ci-pipelines.md).

> **Type:** reference · **Audience:** developer · **Last reviewed:** 2026-06-11

## Go tests (control-api)

Coverage map from the knowledge graph's `tested_by` edges (17 edges over 12 production files):

| Test file | Production files covered | Focus |
|---|---|---|
| [main_test.go](../control-api/main_test.go) | [main.go](../control-api/main.go), [state.go](../control-api/state.go), [policy.go](../control-api/policy.go), [change_safety.go](../control-api/change_safety.go) | Handler routing, app-module generation, action confirmation/blocking, role gates, the "no direct-main mutation" invariant |
| [change_config_test.go](../control-api/change_config_test.go) | [change_config.go](../control-api/change_config.go) | Config-change PR gateway |
| [change_gateway_compose_test.go](../control-api/change_gateway_compose_test.go) | [change_gateway.go](../control-api/change_gateway.go) | Compose-mode app changes through the PR gateway |
| [change_safety_test.go](../control-api/change_safety_test.go) | [change_safety.go](../control-api/change_safety.go) | Change safety checks |
| [change_lifecycle_v06_test.go](../control-api/change_lifecycle_v06_test.go) | [main.go](../control-api/main.go) | v0.6 change lifecycle endpoints |
| [install_test.go](../control-api/install_test.go) | [main.go](../control-api/main.go) | Install-related handlers |
| [drift_test.go](../control-api/drift_test.go) | [drift.go](../control-api/drift.go) | Drift detection |
| [library_v05_test.go](../control-api/library_v05_test.go) | [library.go](../control-api/library.go) | v0.5 catalog/library endpoints |
| [multihost_test.go](../control-api/multihost_test.go) | [platform.go](../control-api/platform.go), [observability.go](../control-api/observability.go) | Multi-host platform merge and observability defaults |
| [platform_test.go](../control-api/platform_test.go) | [platform.go](../control-api/platform.go), [policy_engine.go](../control-api/policy_engine.go) | Platform manifest validation |
| [policy_engine_v03_test.go](../control-api/policy_engine_v03_test.go) | [policy_engine.go](../control-api/policy_engine.go) | v0.3 policy engine rules |
| [storage_policy_v04_test.go](../control-api/storage_policy_v04_test.go) | policy engine (storage rules) | v0.4 "Durable": durable data on ephemeral classes is an explained error |
| [ui_endpoints_test.go](../control-api/ui_endpoints_test.go) | [system.go](../control-api/system.go) | UI-facing endpoints; uses the `asAdmin` helper (loopback + CSRF + an oauth2-proxy identity mapped to admin) to exercise privileged handlers |
| [v2_units_test.go](../control-api/v2_units_test.go) | secrets status / v2 unit generation | Asserts secret *values* never leak into status payloads |

Notable cross-cutting test: `TestControlAPINoExposedDirectMainAppMutation` (main_test.go) reads `main.go` and fails if it ever references `bin/apply.sh` or `bin/app-rollback.sh` — pinning the PR-first change model (see [reference-bin-internals.md](reference-bin-internals.md)).

Run locally (module root is `control-api/`, `go 1.22` minimum in [go.mod](../control-api/go.mod); CI uses Go 1.26):

```bash
cd control-api
go vet ./...
go test ./...                       # full suite
go test -run TestMeHandler ./...    # single test
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
gofmt -l .                          # must print nothing
```

## Eval-time Nix tests (tests/)

[tests/default.nix](../tests/default.nix) wires each test into `checks.${system}` in [flake.nix](../flake.nix) by forcing evaluation of an expression that throws on failure (the result lands in a trivial `runCommand` derivation).

| Test | What it checks |
|---|---|
| [app-model.nix](../tests/app-model.nix) | The app model normalizer (`lib/`) |
| [storage.nix](../tests/storage.nix) | Storage path resolver (`lib/storage.nix`) |
| [multi-host.nix](../tests/multi-host.nix) | Multi-host platform merge (`lib/load-platform.nix`) and opt-in observability defaults |
| [catalog.nix](../tests/catalog.nix) | Catalog schema rules (v0.5), mirroring the assertions in [modules/platform.nix](../modules/platform.nix) so a bad catalog fails eval, not a host build |

Run locally (impure: the flake reads `.env`):

```bash
cp .env.example .env        # if you have no real .env
nix flake check --impure --no-build
```

`--no-build` evaluates everything (including module assertions for every host) without building the heavy VM test below.

## Restore end-to-end VM test

[tests/restore-e2e.nix](../tests/restore-e2e.nix) is a NixOS VM test (needs a build + KVM) proving the backup→restore cycle round-trips: seed a dummy app state dir, restic backup + forget/prune with the exact flags [modules/backup.nix](../modules/backup.nix) generates, destroy and restore the state asserting byte-identical content, run the restore-test and `--read-data-subset=5%` verify patterns, then drive every [bin/backup.sh](../bin/backup.sh) verb and assert the `backups.json` state it writes for the control-api. Known limitation (documented in the file header): the per-app generated systemd units are not covered.

CI triggers (knowledge-graph `triggers` edges): the `restore-e2e` job in [checks.yml](../.github/workflows/checks.yml) on every PR/push to main, and the gating `restore-e2e` job in [release.yml](../.github/workflows/release.yml) — a version tag publishes no release unless it passes.

Run locally (requires KVM, Linux):

```bash
nix build --impure -L .#checks.x86_64-linux.restore-e2e
```

## Suite-to-CI summary

| Suite | Local command | CI job |
|---|---|---|
| Go unit tests | `cd control-api && go test ./...` | checks.yml `go` |
| Eval-time Nix tests | `nix flake check --impure --no-build` | checks.yml `platform` (per host), deploy.yml `check` |
| Restore e2e VM | `nix build --impure -L .#checks.x86_64-linux.restore-e2e` | checks.yml `restore-e2e`, release.yml `restore-e2e` |

## Related pages

- [reference-ci-pipelines.md](reference-ci-pipelines.md) — job graphs and gating
- [backups.md](backups.md) — the backup system the e2e test exercises
