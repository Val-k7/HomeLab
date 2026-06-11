# Web UI: screens reference

Reference for every screen in [web/src/screens/](../web/src/screens), grouped by function: operate (act on apps and the host), observe (read-only insight), and configure (admin-level settings and structured dialogs). UI strings are French; this documentation is English.

> **Type:** reference · **Audience:** developer · **Last reviewed:** 2026-06-11

Endpoint contracts are in [api.md](api.md); screen captures in [screenshots.md](screenshots.md); shell, hooks, and the double-confirm flow in [reference-web-api-layer.md](reference-web-api-layer.md). Two action families recur: **PR changes** (`/v1/changes/*` — durable, merged via Git) and **runtime actions** (immediate, audited; the riskiest go through server-side double-confirm).

## Operate

### Apps — [Apps.tsx](../web/src/screens/Apps.tsx)

Operational hub for installed applications: desired vs runtime state, drift, available updates.

- **Hooks:** `useAppsState`, `useUpdates`, `usePost(["apps-state", "changes"])`; direct `apiGet`/`apiPost` for logs and bulk ops.
- **Actions:** PR changes — `app-update`, `app-rollback` (target + reason dialog), `app-remove` (confirm with reason), `app-policy` (criticality / update policy dialog; one PR per changed field). Runtime — service restart via `/v1/action`, health check via `/v1/health/check`, log viewer via `/v1/logs?app=`.
- **Notable:** bulk restart/health-check runs sequentially per app to avoid hammering systemd; opens [AddAppDialog](../web/src/screens/AddAppDialog.tsx) for manual app creation.

### Library — [Library.tsx](../web/src/screens/Library.tsx)

Workshop catalogs and the modules they offer; install path for catalog apps.

- **Hooks:** `useLibrary`, `usePost(["library", "changes"])`.
- **Actions:** PR — `app-install`, `catalog-remove` (confirm; already-installed modules stay in place). Runtime — browse a catalog (`/v1/library/catalog/:id`), refresh its server-side clone cache (`/v1/library/refresh`) to pick up force-pushed tags without redeploying.
- **Notable:** opens [AddCatalogDialog](../web/src/screens/AddCatalogDialog.tsx) for adding or editing a catalog.

### Changes — [Changes.tsx](../web/src/screens/Changes.tsx)

The PR pipeline: every pending change, its CI status, and its lifecycle controls.

- **Hooks:** `useChanges`; direct `apiGet`/`apiPost` for everything else.
- **Actions:** view diff (`/v1/changes/diff?id=`), refresh from GitHub (`/v1/changes/refresh`), merge (`/v1/changes/merge`), close (`/v1/changes/close`), retry a failed change (`/v1/changes/retry`), prune dead/failed entries (`/v1/changes/prune`).
- **Notable:** normalizes GitHub's status-check rollup (CheckRun vs StatusContext field names) into a flat per-run list with links to logs; computes an overall CI state (success / failure / pending) per change.

### Backups — [Backups.tsx](../web/src/screens/Backups.tsx)

Backup coverage and restic operations per app.

- **Hooks:** `useBackups`, `usePost(["backups"])`; `apiGet` for `/v1/backups/logs`.
- **Actions:** runtime — `/v1/backups/{op}` for backup, restore (danger-styled confirm: overwrites data irreversibly; snapshot picker), and restore-test (sandboxed, non-destructive). Log viewer dialog.
- **Notable:** computes coverage KPIs (required vs covered, critical apps protected, restore-tested count) and warns when the repository or password is not configured.

### System — [System.tsx](../web/src/screens/System.tsx)

Host-level operations: deploy, OS rollback, reboot, plus platform manifest details.

- **Hooks:** `useSystem`, `usePlatform`, `useGenerations` (rollback dialog), `usePostConfirm(["system"])`.
- **Actions (all double-confirmed server-side):** deploy switch (`/v1/deployments` mode `switch`), generation rollback (`/v1/deployments` mode `rollback` — picker lists real NixOS generations with date and version instead of a blind number), reboot (`/v1/reboot`). Runtime — force drift refresh (`/v1/drift?refresh=1`).

## Observe

### Overview — [Overview.tsx](../web/src/screens/Overview.tsx)

Landing dashboard aggregating every health signal into one verdict.

- **Hooks:** `useSystem`, `useAppsState`, `usePolicies`, `useSecrets`, `useBackups`, `useChanges`.
- **Actions:** none (read-only).
- **Notable:** hero banner aggregates issues (disk, down apps, policy violations, missing secrets, uncovered backups, drift, failed changes) into ok/warn/bad; metric cards deep-link to the relevant screens via hash navigation; shows a "behind main" banner pointing at the System screen.

### Security — [Security.tsx](../web/src/screens/Security.tsx)

Policy compliance: per-app permissions, violations with severity and suggested fixes, global policies.

- **Hooks:** `usePolicies`, `useAppsState`.
- **Actions:** none mutating (read-only; "Éditer les politiques" links to Settings).
- **Notable:** renders policy values human-readably (booleans as badges, scalars as chips, objects as compact JSON) with a raw-JSON fold for the full config.

### Monitoring — [Monitoring.tsx](../web/src/screens/Monitoring.tsx)

Internal observability sourced entirely from the control-api (no external metrics system): global roll-up, control-plane infrastructure, per-app runtime.

- **Hooks:** `useObservability`; `apiGet` for logs.
- **Actions:** app log viewer (`/v1/logs?app=`) and infra journal viewer (`/v1/logs/infra?unit=`) limited to a client-side mirror of the server allowlist (control-api, oauth2-proxy, docker, tailscaled).

### Audit — [Audit.tsx](../web/src/screens/Audit.tsx) *(min role: operator)*

Audit trail of every operation with result/risk filters.

- **Hooks:** `useAudit({limit: 300, result, includeUi})`; `apiPost` for prune.
- **Actions:** prune audit + deployment history (`/v1/audit/prune`, confirmed).
- **Notable:** server-side filters (result, include UI reads) plus a client-side full-text filter; client-side CSV/JSON export of the filtered set; events arrive newest-first from the backend.

## Configure

### Storage — [Storage.tsx](../web/src/screens/Storage.tsx)

Storage classes, per-app volumes, and orphaned data directories.

- **Hooks:** `useStorage`, `useStorageOrphans`, `usePost(["storage", "changes"])`, `usePostConfirm(["storage-orphans"])`.
- **Actions:** PR — add a class (via [AddStorageClassDialog](../web/src/screens/AddStorageClassDialog.tsx)), `storage-class-remove`, `app-storage` (move a volume to another class). Runtime, double-confirmed — purge an orphaned app's data (`/v1/apps/purge-data`, danger-styled confirm).
- **Notable:** the default class and classes still in use cannot be removed (the button is replaced by a label).

### Secrets — [Secrets.tsx](../web/src/screens/Secrets.tsx) *(min role: maintainer)*

SOPS secret status for the host (backup, alerting, auth, tailnet) and per-app secrets.

- **Hooks:** `useSecrets`, `useSystemSecrets`, `usePost(["secrets", "system-secrets", "changes"])`.
- **Actions:** PR — `system-secret` (set/rotate a host secret), `app-secret` (set/rotate app values). Both are write-only: values are never read back or displayed, only presence status and last rotation date.

### Settings — [Settings.tsx](../web/src/screens/Settings.tsx) *(min role: admin)*

Three tabs: access, configuration, catalogs. Everything here lands as a PR.

- **Hooks:** `useLibrary` (catalogs tab); direct `apiGet`/`apiPost` everywhere else.
- **Tabs and actions:**
  - **Accès & rôles** — grant/revoke roles via `/v1/changes/access-role` (edits `config/access.json` by PR).
  - **Configuration** — structured form editors for `config/platform.nix` (`platform-config`) and `config/policies.nix` (`policy-config`): values are regex-spliced into the fetched file so comments and layout survive, with a `nixSafe` guard mirroring server validators. An advanced fold offers raw editing of the allowlisted files.
  - **Catalogues** — table of workshop catalogs with add/edit (via [AddCatalogDialog](../web/src/screens/AddCatalogDialog.tsx)) and remove (`catalog-remove`).

### AddAppDialog — [AddAppDialog.tsx](../web/src/screens/AddAppDialog.tsx)

Manual app creation across four runner modes (OCI image, Docker Compose, process build from git, Dockerfile from git); the mode drives which fields are required, mirroring server-side validation.

- **Hooks:** `usePost(["apps-state", "changes"])`; `apiPost` for preview.
- **Actions:** dry-run preview (`/v1/changes/app-add/preview` — shows generated Nix, risk level, warnings) then PR creation (`/v1/changes/app-add`).
- **Notable:** the `Field` helper is declared at module level so its identity is stable — declaring it inside the dialog would remount on every keystroke and steal input focus.

### AddCatalogDialog — [AddCatalogDialog.tsx](../web/src/screens/AddCatalogDialog.tsx)

Structured add/edit of a workshop catalog entry in `config/catalogs.nix`.

- **Actions:** `apiGet` of the config file (early validation), then `/v1/changes/catalog-add` or, in edit mode, `/v1/changes/catalog-update`. The server validates every field.

### AddStorageClassDialog — [AddStorageClassDialog.tsx](../web/src/screens/AddStorageClassDialog.tsx)

Structured add of a storage class to `config/platform.nix`.

- **Actions:** `/v1/changes/storage-class`. The server validates fields (allowlist regexes, `nixString` escaping) and generates the Nix itself; the client never submits generated Nix.
- **Notable:** fetches the config file only to warn early when the `storageClasses` block is missing; the local preview mirrors the server's escaping (`nixStr` mirror of control-api `nixString`) so what you see is what gets spliced.
