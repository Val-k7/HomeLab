# GIT_TOKEN — required permissions

Required permissions for the GitHub fine-grained PAT (`GIT_TOKEN`) used by the control plane.

> **Type:** reference · **Audience:** operator · **Last reviewed:** 2026-06-11

The control plane uses a single GitHub token (fine-grained PAT), provided via the
**`GIT_TOKEN`** GitHub Actions secret and provisioned at deploy time into
`/var/lib/homelab-secrets/git_token`. Rotation: change the `GIT_TOKEN` secret
then run a new deploy — never edit sops.

On the host, `control-api` reads the file pointed to by `HOMELAB_GIT_TOKEN_FILE`
(set to `/var/lib/homelab-secrets/git_token` by `modules/control-api.nix`;
without this environment variable, the hard-coded default is `/run/secrets/git_token`).

## Repository access

`Only select repositories` → `Val-k7/HomeLab` (add any other repository
the control plane needs to manage).

## Repository permissions

| Permission        | Access           | Used by |
|-------------------|------------------|-------------|
| Metadata          | Read-only        | mandatory (enforced by GitHub) |
| Contents          | Read and write   | `git fetch` (deploy + change repo), `git push` of change branches, branch deletion on merge |
| Pull requests     | Read and write   | `gh pr create` / `list` / `view` (mergeable, reviewDecision) / `merge` |
| Actions           | Read-only        | CI column — the server lists the head commit's workflow runs via REST (`gh run list --commit`) |
| Commit statuses   | Read-only        | legacy statuses (non-Actions contexts); rarely useful on its own |

> **GitHub limitation**: the GraphQL `statusCheckRollup` field (check runs) is
> NOT accessible to fine-grained PATs, regardless of permissions —
> only the REST Actions path works. The control-api tries GraphQL first
> (useful if the token ever becomes a classic PAT or a GitHub App) then
> automatically falls back to REST.
| Workflows         | Read and write   | optional — only required if a control plane PR modifies `.github/workflows/` |

No other permission is needed (Issues, Actions, Deployments,
Environments: no).

## Symptoms of a missing permission

| Error | Missing permission |
|---|---|
| `Resource not accessible ... (repository.pullRequests)` | Pull requests: Read |
| `Resource not accessible ... (createPullRequest)` | Pull requests: Write |
| `Resource not accessible ... (statusCheckRollup)` | Checks / Commit statuses: Read (the UI degrades: empty CI column + warning) |
| `Invalid username or token` on `git fetch`/`push` | Contents, or token missing/empty on the host |
| `404` on the private repo via API | repo not in the token's repository selection |
