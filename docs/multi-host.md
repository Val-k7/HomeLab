# Multi-host (the fleet)

How the platform scales from a single host to a fleet: each host is declared under `hosts/<name>/`, with `homelab` remaining the backwards-compatible default.

> **Type:** explanation · **Audience:** operator · **Last reviewed:** 2026-06-11

> The platform scales from a single host named `homelab` to a fleet of
> N hosts, each declared under `hosts/<name>/`. `homelab` stays the default host,
> so a plain single-host setup keeps working with no changes.

## How a host is discovered

`flake.nix` enumerates every directory under `hosts/` that contains a
`configuration.nix` and emits one `nixosConfigurations.<name>` per host:

```text
hosts/
├── homelab/
│   ├── configuration.nix        # required — makes it a host
│   ├── hardware-configuration.nix
│   ├── .env                     # optional per-host env (else repo-root .env)
│   ├── .env.example             # optional per-host example
│   └── platform.nix             # optional per-host platform overlay
└── edge/
    └── ...
```

Drop a new `hosts/<name>/` and it becomes `nixosConfigurations.<name>` with no
edit to `flake.nix`.

## Per-host `.env`

The flake resolves a host's `.env` in this order (read at eval time, so all Nix
commands pass `--impure`):

1. `$HOMELAB_ENV` — explicit override (set by `bin/deploy.sh`, single target).
2. `hosts/<name>/.env` — the per-host file (multi-host layout).
3. `./.env` — repo-root fallback (the single-host layout).

The repo-root fallback is what keeps `homelab` deploying unchanged while its
`.env` still lives at the repo root.

## Per-host platform overlay

`config/platform.nix` is the shared base. A host may override any field with
`hosts/<name>/platform.nix`, deep-merged on top by `modules/platform.nix`. The
published `host.hostname` always tracks the flake host name unless the overlay
sets it explicitly, so `control-api` on each host reports which host it is
(`GET /v1/status` → `host`, `GET /v1/system` → `host`).

## Building / deploying a specific host

```bash
# Local build of a host other than homelab:
sudo HOMELAB_ENV="$PWD/hosts/edge/.env" \
  nixos-rebuild switch --flake .#edge --impure

# On-host deploy script targets a host via HOMELAB_HOST:
HOMELAB_HOST=edge bash bin/deploy.sh /home/admin/homelab switch
```

`bin/deploy.sh` maps `HOMELAB_HOST` to the `#<name>` flake attribute and reads
`hosts/<name>/.env` (falling back to the repo-root `.env` for `homelab`).

## CI

- **checks.yml** — a `discover` job lists every `hosts/<name>/` and the
  `platform` job runs as a matrix, flake-checking and validating each host.
- **deploy.yml** — the fleet is a matrix built from the repository variable
  `FLEET` (a JSON array). Unset, it defaults to a single `homelab` entry driven
  by the existing flat secrets (the single-host default). Example:

  ```json
  [
    {"host":"homelab"},
    {"host":"edge","ssh_host":"edge","ssh_user":"admin","remote_dir":"/home/admin/homelab"}
  ]
  ```

  Each entry may override `ssh_host` / `ssh_user` / `remote_dir`; omitted fields
  fall back to the `SSH_HOST` / `SSH_USER` secrets. A `workflow_dispatch` `host`
  input narrows a run to one host. Each host deploys under its own
  `deploy-<host>` concurrency group, so different hosts deploy in parallel while
  a single host never has two in-flight deploys.

## Adding a new host — checklist

1. `mkdir hosts/<name>` and add `configuration.nix` (import the modules you want)
   plus `hardware-configuration.nix` for that machine.
2. Add `hosts/<name>/.env` (and `.env.example`) with that host's identity and
   networking.
3. Optionally add `hosts/<name>/platform.nix` to override storage classes,
   backup repo, etc.
4. Add an entry to the `FLEET` repository variable so CI deploys it.
5. `nix flake check --impure` (or push and let CI validate the matrix).
