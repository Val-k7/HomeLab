# Contributing

This guide describes the recommended workflow for contributing to the HomeLab
repository.

> **Type:** how-to · **Audience:** contributor · **Last reviewed:** 2026-06-11

## Principles

- Never commit a secret in clear text.
- Keep `.env` local and ignored.
- Limit changes to the responsibility of the file being modified.
- Run the relevant commands before opening a pull request.
- Document any change in operational behavior.

## Recommended Git Workflow

1. Update `main`.
2. Create a working branch.
3. Modify code, configuration, or documentation.
4. Run the relevant checks.
5. Commit with a clear message.
6. Open a pull request.

```bash
git checkout main
git pull --ff-only
git checkout -b feature/<topic>
```

## Branch Convention

Convention detected in the scripts:

| Branch | Usage |
| --- | --- |
| `app-create/<app>-<timestamp>` | Branch created by `bin/app-create.sh` when `deploy_mode` is not `switch`. |
| `main` | Deployment branch watched by GitHub Actions. |

General convention to be finalized by the team. Recommendation:

| Prefix | Recommended usage |
| --- | --- |
| `feature/` | New feature. |
| `fix/` | Fix. |
| `docs/` | Documentation. |
| `infra/` | NixOS, CI/CD, or network configuration. |

## Commit Convention

No formal convention is enforced by the code. The scripts do, however, generate
messages such as:

```text
apps: add <app>
apply: bump <app> <old> -> <latest>
rollback: <app> <old> -> <rev>
```

Recommendation: use short, explicit messages:

```text
docs: update api guide
fix: harden control-api policy
infra: add app declaration
```

## Code Standards

### Nix

- Keep modules separated by responsibility.
- Use `lib.mkIf`, `lib.mkMerge`, and the existing helpers where it is coherent.
- Read `.env` values via `lib/env-lib.nix`.
- Do not duplicate `.env` parsing.

### Go

- Keep the API on the standard library as long as the code allows.
- Add tests in `control-api/main_test.go` or a dedicated test file.
- Respect the existing policies for risky actions.
- Do not widen the allowlist without documenting the risk.

### Shell Scripts

- Keep `set -euo pipefail`.
- Avoid printing secrets.
- Validate arguments and modes.
- Prefer deterministic, loggable commands.

### Documentation

- Use GitHub-flavored Markdown.
- Update `README.md` and the relevant `docs/` guide when behavior changes.
- Add a `TODO / open items` section when operational information is missing.

## Pre-PR Checks

Check `.env`:

```bash
bash bin/check-env.sh .env.example .env
```

Test the Go API:

```bash
cd control-api
go test ./...
```

Evaluate the flake if Nix is available:

```bash
HOMELAB_ENV="$PWD/.env" nix flake check --impure --no-build
```

Build without switching if the environment allows:

```bash
sudo HOMELAB_ENV="$PWD/.env" nixos-rebuild build --flake .#homelab --impure
```

If the PR touches documentation, check links and follow the [style guide](STYLE.md):

```bash
bash bin/docs-check.sh
```

Documentation changes ship in the same PR as the code they describe. New or removed pages
must also update `docs/index.md` and `wiki-staging/_Sidebar.md`.

## Pull Request Process

A pull request should include:

- the goal of the change;
- the main files modified;
- the operational impact;
- the tests or checks that were run;
- known risks;
- useful screenshots or logs if the change touches the UI.

Example:

```md
## Goal

Adds an app declared in `apps/demo.nix`.

## Checks

- `bash bin/check-env.sh .env.example .env`
- `HOMELAB_ENV="$PWD/.env" nix flake check --impure --no-build`

## Risks

- New port exposed on `tailscale0`.
```

## Contributing Apps

To add an app manually:

1. Create `apps/<name>.nix`.
2. Choose a supported runner: `process`, `compose`, or `dockerfile` (plus the
   `image` runner for V2 units).
3. Declare `port` if the app must be reachable.
4. Run a Nix evaluation.

Example `process`:

```nix
{
  runner = "process";
  repo = "https://github.com/you/your-node-app";
  rev = "0000000000000000000000000000000000000000";
  runtime = "nodejs_22";
  buildCmd = "npm ci && npm run build";
  startCmd = "node dist/index.js";
  port = 3000;
}
```

## TODO / open items

- Official branch convention for human contributions.
- Official commit convention.
- Minimum review rules based on risk level.
- Versioning or changelog policy.
