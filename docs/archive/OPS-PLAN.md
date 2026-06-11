# OPS-PLAN — Grafana as a local ops control plane

Turn the existing Grafana into a single pane that not only observes but
acts: control services and containers, trigger the gated deploy/rollback
pipeline locally on the host, and surface available updates — all
tailnet-only, no GitHub token, git remaining the source of truth.

This plan is the reference for the Ops feature. It is phased; each phase
is independently deployable and leaves the system working.

---

## 1. Goals and principles

1. **One pane.** Grafana observes (existing dashboards) and acts (new Ops
   dashboard with buttons).
2. **Local execution.** Deploy/rollback run on the host via the same gate
   as CI (`build -> test -> switch`). No GitHub API, no token.
3. **Git stays the truth.** Buttons trigger transient, reversible actions.
   Permanent change = edit `apps/`/modules + push. Applying an app-version
   update means changing the pinned `rev` in git (manual or Renovate),
   never a host-side git write.
4. **Tailnet-only, allowlisted.** No control endpoint on WAN/LAN. Only a
   fixed allowlist of targets/operations. No token (tailnet identity is
   the auth boundary).
5. **Survive self-restart.** Any action that rebuilds the system must be
   detached from the control-api lifecycle (`systemd-run`), or it kills
   itself mid-switch.

---

## 2. Architecture

```
Browser (tailnet) ── Grafana
   observe: System / Apps / Services dashboards (Prometheus)
   act:     Ops dashboard (canvas buttons -> HTTP POST)
                     │
                     ▼
            control-api (Go, :9092, tailnet-only)
              /v1/action   service|container start/stop/restart
              /v1/reboot
              /v1/deploy    systemd-run bin/deploy.sh
              /v1/rollback  systemd-run bin/rollback.sh <gen>
              /v1/status    current gen, last deploy state
              /metrics      gen, last-deploy status/time
                     │ validate allowlist → sudo (exact cmds)
                     ▼
            systemctl / docker / nixos-rebuild / reboot
                     ▲
            bin/deploy.sh, bin/rollback.sh  ← shared with .github CI

            update-exporter (Go, :9102, read-only)
              reads /etc/homelab/apps.json
              git ls-remote / registry digest → compare to pinned
              /metrics  homelab_app_update_available{...}
                     ▲ scraped by Prometheus → Ops "Updates" table
```

Both `control-api` and `update-exporter` expose `/metrics`, scraped by the
existing Prometheus. Grafana reads everything through the Prometheus
datasource (no extra datasource plugin). Buttons use native Grafana
**Canvas actions** (API call on click); fallback is a button-panel plugin.

---

## 3. Components

### 3.1 control-api (Go)

- Source in repo: `control-api/` (`main.go`, `go.mod`), built with
  `pkgs.buildGoModule` in `modules/control-api.nix`, run as a hardened
  systemd service under a dedicated user `controlapi`.
- Listen `0.0.0.0:9092`; reachable on the tailnet only because the
  firewall opens nothing and `tailscale0` is trusted.
- Endpoints:
  - `POST /v1/action` body `{ kind: "service"|"container", target, op }`
    where `op ∈ {start,stop,restart}`. Quick exec (does not touch
    control-api itself).
  - `POST /v1/reboot`.
  - `POST /v1/deploy` → `systemd-run --no-block --unit=hl-deploy
    /run/.../bin/deploy.sh`.
  - `POST /v1/rollback` body `{ generation? }` → `systemd-run ...
    bin/rollback.sh <gen>`.
  - `GET /v1/status` → current generation, `hl-deploy` unit state, last
    result.
  - `GET /metrics` → `hl_current_generation`, `hl_last_deploy_status`,
    `hl_last_deploy_timestamp`.
- Validation: every request checked against the allowlist before any exec.
  Unknown target/op → 400, logged.
- Audit: every accepted action logged to journald with caller IP.

### 3.2 Shared gate scripts

- `bin/deploy.sh`: `git fetch origin main && git reset --hard
  origin/main` → `bash bin/check-env.sh` → `nixos-rebuild build` →
  `nixos-rebuild test` → `nixos-rebuild switch`. Exit non-zero aborts
  before `switch`.
- `bin/rollback.sh <gen?>`: empty → `nixos-rebuild switch --rollback`;
  else `nix-env --switch-generation <gen>` + `switch-to-configuration`.
- Used by BOTH `.github/workflows/deploy.yml` (push trigger) and
  `control-api` (button trigger) → one gate definition, two triggers.

### 3.3 update-exporter (Go)

- Source `update-exporter/`, built with `buildGoModule`, systemd service
  on `:9102`, read-only (no privilege).
- Reads `/etc/homelab/apps.json` (see 3.4) for each app's `repo`+`rev`
  (and image where applicable).
- Per app:
  - rev-based (`process`/`dockerfile`): `git ls-remote <repo> <branch>` →
    latest commit vs pinned `rev`.
  - image-based (`compose`/`dockerfile` image): registry digest vs running
    (via `skopeo inspect`), phase-3b optional.
- Emits `homelab_app_update_available{app,kind,current,latest} 0|1` and
  caches results on a ticker to avoid hammering upstreams.

### 3.4 Apps manifest

- Parsing `.nix` from Go is fragile. Instead, the Nix side emits the app
  set as JSON: `environment.etc."homelab/apps.json".text =
  builtins.toJSON apps;` (computed from the same `apps/*.nix` scan).
- `update-exporter` reads this manifest — single source, no Nix parsing.

### 3.5 Grafana Ops dashboard

- New provisioned board `modules/dashboards/ops.json`, datasource
  Prometheus.
- Panels:
  - **State**: `hl_current_generation`, last-deploy status/time (stats).
  - **Controls** (Canvas buttons → API call):
    - Deploy/Update → `POST /v1/deploy`
    - Rollback (with a `$generation` variable) → `POST /v1/rollback`
    - Reboot (confirm) → `POST /v1/reboot`
  - **Updates** table: `homelab_app_update_available` (app, current,
    latest, state) with a transformation to a clean table.
  - **Deploy log/state**: from `hl_last_deploy_status` + `/v1/status`.
- Apps/Services boards gain a small **Controls** row (start/stop/restart
  for the selected `$service`/`$container`) calling `/v1/action`.
- Buttons carry the dashboard variable value (e.g. `$service`) in the POST
  body. No secret in the dashboard JSON (tailnet-only auth).

---

## 4. Security model

| Layer | Measure |
|---|---|
| Network | bind reachable on `tailscale0` only (firewall opens nothing on WAN/LAN) |
| Auth | tailnet identity is the boundary; no token (so nothing leaks into dashboard JSON) |
| Allowlist | `op ∈ {start,stop,restart}`; targets = `app-*` units + a configured safe-infra set + docker containers; everything else rejected |
| Lockout guard | `sshd`, `tailscaled`, `networking`, `firewall` are NEVER controllable via the API |
| Sudo | `controlapi` user gets NOPASSWD only for the exact command patterns below |
| Audit | every accepted action logged with caller IP |

Sudoers (exact patterns, no wildcards beyond the app namespace):

```
controlapi ALL=(root) NOPASSWD: /run/current-system/sw/bin/systemctl start app-*.service, \
  /run/current-system/sw/bin/systemctl stop app-*.service, \
  /run/current-system/sw/bin/systemctl restart app-*.service, \
  /run/current-system/sw/bin/systemctl reboot, \
  /run/current-system/sw/bin/systemd-run --no-block --unit=hl-deploy *, \
  /run/current-system/sw/bin/systemd-run --no-block --unit=hl-rollback *
```

Docker control runs via the `docker` group membership of `controlapi`
(no sudo needed), still gated by the API allowlist.

---

## 5. The self-restart gotcha

`control-api` is a systemd unit. If it runs `nixos-rebuild switch`
directly, the switch restarts `control-api.service` and kills the switch
mid-activation → broken generation.

Rule: deploy/rollback are dispatched as **detached transient units**:

```
systemd-run --no-block --unit=hl-deploy   bin/deploy.sh
systemd-run --no-block --unit=hl-rollback bin/rollback.sh <gen>
```

The rebuild then runs independent of `control-api`'s lifecycle. The
dashboard polls `hl-deploy`/`hl-rollback` state via `/v1/status`.

---

## 6. Phases

### Phase 1 — control-api: service/container control + reboot
- `control-api/` Go: `/v1/action`, `/v1/reboot`, `/v1/status`, `/metrics`.
- `modules/control-api.nix`: build, hardened service, sudoers, docker
  group, `:9092` tailnet-only.
- Prometheus scrape `control-api:9092/metrics`.
- Apps/Services dashboards: add Controls row (start/stop/restart selected).
- **Validation**: from a tailnet browser, restart `app-whoami` and a
  container; confirm via the table + journald audit line; confirm
  `sshd`/`tailscaled` are rejected.

### Phase 2 — local gated deploy/rollback
- `bin/deploy.sh` + `bin/rollback.sh`; refactor `deploy.yml` to call them.
- `control-api`: `/v1/deploy`, `/v1/rollback` via `systemd-run`.
- Ops dashboard `ops.json`: state stats + Deploy/Rollback/Reboot buttons +
  deploy-state panel.
- **Validation**: push a trivial change; click Deploy → host converges to
  `origin/main` through the gate; break something → `test` gate aborts
  before `switch`; Rollback restores; confirm control-api survives the
  switch (systemd-run).

### Phase 3 — update detection
- Nix emits `/etc/homelab/apps.json`.
- `update-exporter/` Go on `:9102`; Prometheus scrape.
- Ops dashboard: Updates table from `homelab_app_update_available`.
- Optional 3b: image-digest checks via `skopeo`; Renovate for the PRs.
- **Validation**: pin an app to an old `rev`; the table shows `outdated`
  with current/latest; bump `rev` in git + Deploy → flips to up-to-date.

---

## 7. Risks

| Risk | Mitigation |
|---|---|
| Control endpoint reachable by any tailnet device | tailnet is the trust boundary (homelab); document; optional per-client ACL later |
| Self-restart kills a switch | `systemd-run` detached units (section 5) |
| Restarting a critical unit cuts access | hard exclusion list (`sshd`/`tailscaled`/`networking`/`firewall`) |
| Button mutates outside git | actions are transient; permanent change = git; update-apply = git only |
| Canvas actions insufficient in the Grafana version | fallback to a button-panel plugin (declarative install) |
| update-exporter hammering upstreams | ticker cache, sane interval |

---

## 8. Locked decisions

- Language: **Go** (control-api + update-exporter), single static binaries
  via `buildGoModule`.
- Auth: **tailnet-only, no token**.
- Deploy/rollback: **local**, via shared `bin/*.sh`, dispatched with
  `systemd-run` (no GitHub token).
- Update apply: **git only** (manual rev bump or Renovate); dashboard is a
  read-only radar for app versions.
- Critical units (`sshd`/`tailscaled`/`networking`/`firewall`) are never
  controllable.

---

## 9. Open questions

1. Safe-infra services controllable beyond `app-*` (grafana, prometheus,
   cadvisor, docker)? Define the exact set.
2. Grafana version's Canvas actions vs a button-panel plugin — confirm at
   phase 2.
3. Rollback UX: free generation number input, or a dropdown populated from
   a `node`/control-api metric listing generations?
4. update-exporter default check interval (e.g. 30 min) and which branch
   to compare against per app (default `main`, overridable in `apps/*.nix`).
```
