# Remediation Plan — security audit 2026-06-08

Fix plan for every finding in the project audit, ordered by exploitability. Each
item: what to change, which files, how to verify, effort (S ≤1h / M ≤½day / L >½day).

**Out of scope (intentionally skipped):** Prometheus / node-exporter / Grafana /
Loki observability stack (`modules/metrics.nix`, `modules/observability.nix`) — not
used in this deployment, so the `listenAddress = 0.0.0.0` hardening (audit LOW) is
dropped rather than fixed.

Guiding principle of the two root-access fixes: the privilege-escalation risk comes
from granting a *network-facing service user* sudo to a **generic command executor**
(`systemd-run`) or membership of a root-equivalent group (`docker`). The fix is to
interpose **fixed, trusted wrapper scripts** that validate their inputs and exec a
hard-coded action — then the sudo grant points at the wrapper, not the executor.

---

## Phase 1 — Close root (CRITICAL). Do first, ship as one PR.

### P1 — Replace the `systemd-run … *` sudo wildcards with fixed wrappers
- **Finding:** [control-api.nix:49-52](../modules/control-api.nix) — `sudo systemd-run --unit=hl-deploy *` lets the `controlapi` user run any command as root.
- **Change:**
  1. Add Nix-installed wrapper scripts at fixed paths (`environment.etc."homelab/bin/hl-deploy"` etc., or `pkgs.writeShellScriptBin`), one per job class: `hl-deploy`, `hl-app-create`, `hl-backup`. Each starts with `set -euo pipefail`, validates every arg against a strict regex (mode ∈ {dry-run,build,switch,rollback}; target matches `^[a-zA-Z0-9:._/@-]{0,160}$`; app matches `^[a-z0-9-]+$`), then `exec`s the real work under a transient unit it builds itself (the script — not the caller — chooses the `systemd-run` command line).
  2. Rewrite the sudoers block to grant **only** those wrapper paths, no trailing `*` on a generic executor:
     ```
     controlapi ALL=(root) NOPASSWD: /etc/homelab/bin/hl-deploy, \
                                     /etc/homelab/bin/hl-app-create, \
                                     /etc/homelab/bin/hl-backup
     ```
     (Args are still accepted, but they reach trusted wrapper code that re-validates them, so they can no longer select an arbitrary program.)
  3. Update the Go call site [deployments.go:118-125](../control-api/deployments.go) to invoke `sudo /etc/homelab/bin/hl-deploy <mode> <target>` instead of `sudo systemd-run … bash deploy.sh …`. Same for the app-create and backup call sites ([change_gateway.go] commandRunner users, [backups.go]).
- **Verify:** `sudo -l -U controlapi` shows only the three wrapper paths. Manually attempt `sudo /etc/homelab/bin/hl-deploy 'switch; id'` → wrapper rejects (bad mode), no shell exec. A normal deploy still works end-to-end.
- **Effort:** L.

### P2 — Remove `controlapi` from the `docker` group
- **Finding:** [control-api.nix:36](../modules/control-api.nix) — docker group = root-equivalent (mount host `/`).
- **Change:** Drop `extraGroups = [ "docker" ]`. Mediate the container actions the API needs (`start`/`stop`/`restart` in [main.go:118-126](../control-api/main.go); `ps`/`inspect` in [targets.go], [main.go:171-186](../control-api/main.go)) through a fixed `hl-container` sudo wrapper (validates op ∈ {start,stop,restart} and name against `reContName`, then execs `docker <op> <name>`), plus a read-only path for `ps`/`inspect` (same wrapper pattern or a read-only `docker-socket-proxy`). Update the Go exec calls to go through the wrapper.
- **Verify:** `id controlapi` no longer lists `docker`. `docker ps` as controlapi fails directly but the `/v1/targets` and `/v1/action` container paths still work via the wrapper.
- **Effort:** M.

### P3 — Kill the `sed` / repo-URL injections in the deploy scripts
- **Findings:** [app-rollback.sh:37](../bin/app-rollback.sh) + [apply.sh:37](../bin/apply.sh) unanchored `sed -i "s/${OLD}/${REV}/"` (rev regex permits `/ & \` → rewrites arbitrary `.nix` that is then built as root); [apply.sh:29](../bin/apply.sh) `git ls-remote "$REPO"` with unvalidated/​option-injectable URL.
- **Change:**
  1. Replace the `sed` rewrite with an anchored, literal replacement that only touches the `rev = "…"` / `tag = "…"` line — e.g. a small `python3 -c` or `awk` that does a fixed-string (not regex) swap on the matched key line. Reject any rev containing `/`, `"`, `\`, `&`, newline before use.
  2. Allowlist the repo URL: `case "$REPO" in https://github.com/*) ;; *) echo "bad repo"; exit 1;; esac` and add `--` before positional git args.
  3. Add `set -euo pipefail` to any script missing it.
- **Verify:** Feed a crafted rev like `a/b" ; foo` to the rollback path in a scratch checkout → script aborts, file unchanged. Normal rev still rewrites the one line.
- **Effort:** M.

---

## Phase 2 — Auth front (HIGH). Second PR.

### P4 — Stop accepting every GitHub account
- **Finding:** [auth.nix:30](../modules/auth.nix) `email.domains = ["*"]` + the org allowlist only applied when `OAUTH2_GITHUB_ORG` is set, which `.env.example` never sets.
- **Change:** Make org/team mandatory: assert non-empty `githubOrg` at eval time (`lib.assertMsg (githubOrg != "") "OAUTH2_GITHUB_ORG required"`), or switch to `--authenticated-emails-file` listing the exact allowed emails. Keep `email.domains` narrow (drop `"*"` once an emails-file or org gate is enforced). Set `OAUTH2_GITHUB_ORG` (and ideally `OAUTH2_GITHUB_TEAM`) in `.env.example` with a comment that it is required.
- **Verify:** Re-deploy with org set; a GitHub account outside the org is rejected at oauth2-proxy (302 to error, never reaches control-api). `journalctl -u oauth2-proxy` shows the denial.
- **Effort:** S.

### P5 — Harden the oauth2-proxy cookie
- **Finding:** [auth.nix:32](../modules/auth.nix) — no `cookie-secure` / `cookie-samesite` / `cookie-expire` / `cookie-refresh`.
- **Change:** Add to `extraConfig`: `"cookie-secure" = "true"; "cookie-samesite" = "lax"; "cookie-expire" = "168h"; "cookie-refresh" = "1h";`.
- **Verify:** Response `Set-Cookie` shows `Secure; SameSite=Lax`; session still persists across requests.
- **Effort:** S.

### P6 — Gate the reboot / docker-restart sudo grants behind the hardened front
- **Finding:** [control-api.nix:47-48](../modules/control-api.nix) — `restart docker.service` + `reboot` let the API user DoS the host.
- **Change:** Keep the grants (the UI needs them) but they are only acceptable once P4 lands; confirm the handlers already require `admin` ([main.go:193](../control-api/main.go) reboot) and keep them so. No code change beyond confirming the role floor; document the dependency on P4.
- **Verify:** Reboot/docker-restart still require admin + double-confirm; viewer/operator get 403.
- **Effort:** S.

---

## Phase 3 — CD pipeline (HIGH). Third PR.

### P7 — Restore SSH host-key verification in deploy
- **Finding:** [deploy.yml:11-126](../.github/workflows/deploy.yml) — `StrictHostKeyChecking=accept-new` + `UserKnownHostsFile=/dev/null` (TOFU defeated each run; MITM on first connect sees the `.env` secrets).
- **Change:** Add the host's public key to a committed/known_hosts (or a `secrets.SSH_KNOWN_HOSTS`) and use it; drop `/dev/null`.
- **Verify:** Deploy succeeds with the pinned key; a wrong/missing key aborts the connection.
- **Effort:** S.

### P8 — Lock down the remote `.env` secret transfer
- **Finding:** [deploy.yml:79-107](../.github/workflows/deploy.yml) — ~30 secrets streamed via `ssh "cat > .env"`, remote file may be world-readable.
- **Change:** `umask 077` before writing; `chmod 600` the remote `.env`; avoid line-by-line echo of values (write once from a heredoc). Confirm no `set -x` in the remote payload.
- **Verify:** Remote `.env` is `-rw-------`; runner logs do not contain secret values.
- **Effort:** S.

### P9 — Stop sed-injecting the git token into URLs
- **Findings:** [deploy.sh:44-56](../bin/deploy.sh), [app-create.sh:44](../bin/app-create.sh), [app-rollback.sh:34](../bin/app-rollback.sh) — token spliced into the remote URL via `sed`; breaks on token metachars and exposes the token in `ps`/args. Mirror of the Go helper [change_gateway.go:176-182](../control-api/change_gateway.go).
- **Change:** Use a credential helper / `GIT_ASKPASS`, or `git -c http.extraHeader="Authorization: Bearer $TOKEN"`, instead of embedding the token in the URL. Never build the URL with `sed`.
- **Verify:** `git fetch`/`push` still authenticate; `ps aux` during a push shows no token in the command line.
- **Effort:** M.

### P10 — Validate `workflow_dispatch` inputs
- **Finding:** [rollback.yml:31-37](../.github/workflows/rollback.yml) — `generation` input concatenated raw into an ssh command.
- **Change:** `if ! [[ "$GEN" =~ ^[0-9]+$ ]]; then echo bad; exit 1; fi` and pass `"$GEN"` as a quoted positional arg.
- **Verify:** A non-numeric input fails the job before any ssh; a numeric one rolls back.
- **Effort:** S.

### P11 — Least-privilege `GITHUB_TOKEN`
- **Finding:** No top-level `permissions:` in any workflow → inherits the broad repo default.
- **Change:** Add `permissions: contents: read` at the top of [checks.yml](../.github/workflows/checks.yml), [deploy.yml](../.github/workflows/deploy.yml), [rollback.yml](../.github/workflows/rollback.yml); elevate per-job only where a write is actually needed.
- **Verify:** Workflows still pass; token has read-only contents unless a job opts up.
- **Effort:** S.

### P12 — Pin third-party actions to a SHA
- **Finding:** [deploy.yml:73](../.github/workflows/deploy.yml) / [rollback.yml:23](../.github/workflows/rollback.yml) — `tailscale/github-action@v3` on a moving tag.
- **Change:** Pin `tailscale/github-action` (and ideally `cachix/install-nix-action`, `actions/*`) to full commit SHAs with a `# vX.Y.Z` comment.
- **Verify:** Workflows resolve the pinned SHA; Renovate keeps them updated ([renovate.json](../renovate.json)).
- **Effort:** S.

---

## Phase 4 — API hardening (HIGH). Fourth PR.

### P13 — Add CSRF defense to state-changing requests
- **Finding:** [client.ts:34](../web/src/api/client.ts) — cookie-auth POSTs (deploy/rollback/secret/install) carry no CSRF token / custom header.
- **Change:** Server: require a non-simple header (e.g. `X-HL-CSRF` or enforce `X-Requested-With: fetch`) on every mutating handler — add a small check in `requireMutationAuth` ([authz.go:99](../control-api/authz.go)) so a cross-site `<form>`/simple request without the header is rejected. Client: set `credentials: "same-origin"` and always send the header in `apiPost` ([client.ts](../web/src/api/client.ts)).
- **Verify:** A POST without the header → 403; the UI (which sends it) still works. Cross-site form submission cannot trigger a deploy.
- **Effort:** M.

### P14 — UI confirmation on destructive actions
- **Findings:** [System.tsx:10](../web/src/screens/System.tsx) deploy fires on one click; [System.tsx:13](../web/src/screens/System.tsx) rollback gated only by `window.prompt` with no int validation; [Apps.tsx:79](../web/src/screens/Apps.tsx) restart; [Backups.tsx:31](../web/src/screens/Backups.tsx) restore/backup-now.
- **Change:** Wrap each in an explicit confirm dialog; for deploy/rollback, re-POST the server-returned `confirm_id` (the double-confirm contract already exists server-side). Validate the rollback generation is a positive integer before POST.
- **Verify:** Each destructive button needs a deliberate confirm; an empty/invalid generation is blocked client-side.
- **Effort:** M.

### P15 — Bound every exec with a timeout
- **Finding:** No `exec.Command` in control-api uses a context/timeout — a wedged `journalctl` / `git ls-remote` / `systemd-run` / restic hangs the serving goroutine (DoS). Spread across [deployments.go:99,125](../control-api/deployments.go), [change_gateway.go:32](../control-api/change_gateway.go), [library.go](../control-api/library.go), [logs.go](../control-api/logs.go), [system.go](../control-api/system.go), [main.go:279,291](../control-api/main.go), [backups.go](../control-api/backups.go), [targets.go](../control-api/targets.go).
- **Change:** Introduce one helper `runCtx(timeout, name, args…)` using `exec.CommandContext`; route `commandRunner` and the ad-hoc `exec.Command` calls through it with sane per-class deadlines (reads ~10s, git network ~30s, deploy launch is `--no-block` so fast). Kill the process group on expiry.
- **Verify:** A stubbed slow command returns a timeout error to the handler instead of hanging; normal calls unaffected. Unit test the helper.
- **Effort:** M.

### P16 — Fix the Nix-string injection in generated PRs
- **Findings:** [change_ext.go:162,170](../control-api/change_ext.go) and [change_gateway.go:781](../control-api/change_gateway.go) (`replaceAppVersion`) — value goes into `"`+value+`"`; `validNixAtom` ([apps_api.go:44](../control-api/apps_api.go)) permits `\`, so a trailing `\` escapes the closing quote, and `re.ReplaceAllString` expands `$1`/`${x}` templates.
- **Change:** Emit every scalar through the existing `nixString()` helper ([apps_api.go:35](../control-api/apps_api.go)) instead of hand-built quotes; replace `re.ReplaceAllString` with a literal splice (build the replacement without regex template expansion). Tighten `validNixAtom` to also reject `\` and `$` as defense-in-depth.
- **Verify:** Unit test: a value `1.0\` or `v$1` produces well-formed Nix (escaped or rejected), not a broken string. Existing update/rollback PRs still generate correctly. (Mitigated today only by the PR-review + CI gate before host apply — close it at the source.)
- **Effort:** M.

### P17 — Constant-time service-token compare
- **Finding:** [authz.go:75,78](../control-api/authz.go) — `==` on `CONTROL_API_SERVICE_TOKEN` (timing side-channel).
- **Change:** `subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1` after an equal-length guard.
- **Verify:** Valid token still grants admin; invalid rejected; compare is length-safe.
- **Effort:** S.

---

## Phase 5 — Defense-in-depth (MEDIUM). Fifth PR.

### P18 — Default interactive sudo to require a password
- **Finding:** `SUDO_NEEDS_PASSWORD=false` default ([.env.example:9](../.env.example) → [configuration.nix](../hosts/homelab/configuration.nix)) gives wheel passwordless sudo; one SSH-key compromise = root.
- **Change:** Default to `true`; document that the admin sets a password.
- **Verify:** Interactive `sudo` as the admin user prompts for a password; the controlapi NOPASSWD wrappers (P1) are unaffected.
- **Effort:** S.

### P19 — Catalog git option-injection + timeout
- **Finding:** [library.go:43,50](../control-api/library.go) — `git clone --branch <ref>` / repo positional with no `--` separator or leading-dash check (config-sourced, lower impact) and no timeout.
- **Change:** Validate `c.Ref`/`c.Repo` against a strict regex, add `--` before positional args, and use the P15 timeout helper.
- **Verify:** A catalog entry with a `-`-prefixed ref/repo is rejected; normal clone/fetch works.
- **Effort:** S.

### P20 — Remove the mixed-content app link
- **Finding:** [Apps.tsx:82](../web/src/screens/Apps.tsx) — hardcoded `http://${location.hostname}:${port}` (mixed content, bypasses the proxy origin).
- **Change:** Derive scheme from `location.protocol` or route through the proxy path.
- **Verify:** Link uses https on the tailnet origin; no mixed-content console warning.
- **Effort:** S.

---

## Phase 6 — Hygiene (LOW). Batch into the relevant PRs above.

- **P21** Tailscale auth key into SOPS instead of plain `/etc/tailscale/authkey` ([tailscale.nix:10](../modules/tailscale.nix)); document the assumed tailnet ACL. *(S)*
- **P22** Enable host IP forwarding only if actually used as subnet-router/exit-node ([tailscale.nix:20](../modules/tailscale.nix)). *(S)*
- **P23** Assert the SOPS age key is `root:root 0400` ([secrets.nix:43](../modules/secrets.nix)) via tmpfiles/activation. *(S)*
- **P24** Rotate/tail `audit.jsonl` and `changes.jsonl`; `/v1/audit` currently reads the whole file per request ([state.go:131](../control-api/state.go)). *(M)*
- **P25** Key React lists by a stable id, not array index ([Changes.tsx:33](../web/src/screens/Changes.tsx) + Library/Storage/Security screens). *(S)*
- **P26** Log (don't silently swallow) audit-append write errors ([state.go:115](../control-api/state.go)). *(S)*
- **P27** Once the org gate (P4) is enforced, pin the admin identity in [access.json](../config/access.json) to an org-verified account rather than a bare personal gmail. *(S)*

---

## Suggested PR sequence
1. **PR-1 (Phase 1)** — root closure (P1–P3). Highest impact; review carefully.
2. **PR-2 (Phase 2)** — auth front (P4–P6).
3. **PR-3 (Phase 3)** — CI/CD (P7–P12).
4. **PR-4 (Phase 4)** — API hardening (P13–P17).
5. **PR-5 (Phase 5+6)** — defense-in-depth + hygiene (P18–P27).

Each PR: run `nix flake check` / the Go tests (`control-api/*_test.go`) and a staging deploy ([docs/DEPLOY-STAGING.md](DEPLOY-STAGING.md)) before merge to `main` — remember `main` is built and switched as root by the pipeline.
