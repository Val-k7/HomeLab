# Plan complet — Control Plane standalone (sans Grafana)

UI control plane produit, servie en standalone, derrière Tailscale +
oauth2-proxy. Plus aucune dépendance Grafana. Chaque écran sépare
**desired / runtime / action / risk**. Toute écriture durable = PR ; toute
action runtime = auditée. Les parties sensibles (identité, sessions, crypto)
sont déléguées à des composants éprouvés, jamais codées main.

---

## 0. Décisions verrouillées

| Axe | Choix |
| --- | --- |
| Rendu | React + Vite + shadcn/ui, TanStack Query. Bundle statique. |
| Shell | Standalone. Retrait total de Grafana. |
| Réseau | Tailscale uniquement (jamais d'IP publique brute). |
| Auth | oauth2-proxy (OIDC GitHub) **derrière** Tailscale. 2 couches. |
| Observabilité | Tout retiré (Grafana, Prometheus, Loki, Promtail, Tempo, Pyroscope, cAdvisor, node_exporter). |
| Métriques système | Lues **localement en stdlib** par control-api (`/proc`, statfs). Pas de dépendance Go nouvelle (`vendorHash = null` conservé). |
| Secrets/crypto | SOPS server-side inchangé. Client : jamais valeur ni clé. |

---

## 1. Architecture cible

```
client (tailnet) ──HTTPS──> tailscale serve :443
                               │ (MagicDNS name, TLS auto)
                               ▼
                         oauth2-proxy 127.0.0.1:4180   ← identité (OIDC GitHub, sessions, CSRF)
                               │ injecte X-Forwarded-Email / X-Forwarded-User
                               ▼
                         control-api 127.0.0.1:9092     ← frontière sécu (RBAC, PR-only, audit)
                               ├── /            → bundle React statique (WEB_ROOT)
                               ├── /assets/*    → assets
                               └── /v1/*, /healthz → API
```

Ports : **rien sur tailscale0 ni public**. control-api et oauth2-proxy sont en
loopback ; seul `tailscale serve` expose le 443 sur le tailnet. On retire les
ouvertures firewall control-api(9092)/grafana(3000)/prom(9090)/loki(3100)/…

---

## 2. Modèle de sécurité

- **Couche réseau** : Tailscale. Hors-tailnet → Funnel explicite seulement si
  requis (sinon inaccessible). Pas d'exposition LAN/public directe.
- **Couche identité** : oauth2-proxy authentifie (GitHub OIDC), gère sessions +
  CSRF + cookies. Tout non-authentifié est bloqué **avant** control-api.
- **Couche autorisation** : control-api lit `X-Forwarded-Email` (posé par
  oauth2-proxy) → rôle via `config/access.json` (`users` : email→role). RBAC
  `authz.go` inchangé sur le fond.
- **Anti-spoof** : control-api écoute **loopback only** ; il ne reçoit que le
  trafic d'oauth2-proxy (même hôte). Un client externe ne peut pas injecter de
  faux `X-Forwarded-*` (il ne joint jamais control-api directement).
- **Invariants conservés** : écritures durables = PR ; actions runtime auditées ;
  SOPS server-side ; un frontend compromis ne peut pas dépasser son rôle.

Changement notable : le **token UI** (`X-HL-Token`) et le bypass
`validGrafanaProxyRequest` deviennent inutiles — oauth2-proxy est la porte.
`requireMutationAuth` exige désormais une identité oauth2-proxy présente.

---

## 3. Phasage

Chaque phase laisse le système déployable. **P0 avant le teardown** pour ne
jamais perdre l'accès UI.

### P0 — Socle auth + serving
- Nix : `modules/auth.nix` (oauth2-proxy), `tailscale serve` (systemd oneshot),
  `web` package (buildNpmPackage), control-api sert le bundle.
- Go : `/v1/me` ; `X-Forwarded-Email` dans `actorFromRequest` ; file-server SPA ;
  `requireMutationAuth` basé identité oauth2-proxy ; bind loopback.
- Squelette React minimal (login OK via oauth2-proxy, affiche `/v1/me`).
- **Acceptance** : accès UI via nom MagicDNS en HTTPS ; non-authentifié bloqué ;
  `/v1/me` renvoie email+rôle ; control-api injoignable hors loopback.

### P1 — Teardown observabilité + métriques locales
- Supprimer `modules/monitoring.nix`, `modules/observability.nix`,
  `tools/generate-native-dashboards.go`, `modules/dashboards/*`,
  `control-api/ui.go`, `grafanaStatHandler`, handlers `/v1/grafana*`,
  `/v1/kiosk*`, gauges plateforme + alertes dans `/metrics`.
- Retirer imports dans `hosts/homelab/configuration.nix` + ports firewall.
- Go : `/v1/system` (cpu/mem/disk/load/uptime via `/proc` + statfs, stdlib) ;
  retirer `promQuery` de `overview` (lire `/v1/system`).
- **Acceptance** : `nix flake check` vert sans monitoring/observability ;
  `/v1/system` renvoie des métriques cohérentes ; build Go sans nouvelle dép.

### P2 — Apps (refonte) → `/v1/apps/state`
- Front : écran Apps complet (desired/runtime/storage/secrets/backup/drift/
  policy) + actions (update PR, rollback PR, restart, healthcheck now, logs,
  ouvrir).
- Go : `/v1/logs?app=` (journalctl `app-*`, regex unit safe).
- **Acceptance** : payload vide+peuplé OK ; actions appellent le bon endpoint ;
  séparation PR vs runtime visible.

### P3 — Overview (santé)
- Agrège `/v1/system`, `/v1/apps/state`, `/v1/policies`, `/v1/secrets/status`,
  `/v1/backups`, `/v1/health/apps`.
- **Acceptance** : en 1 écran, état sain/non sain en <10 s.

### P4 — Backups
- Front sur endpoints existants (`/v1/backups` + actions runtime).
- **Acceptance** : couverture + actions auditées.

### P5 — Secrets
- Front `/v1/secrets/status` (jamais de valeur) + add/rotate SOPS via
  `/v1/changes/app-secret`.
- **Acceptance** : aucune valeur affichée ; PR contient du chiffré.

### P6 — Security / Policies
- Lecture `/v1/policies` + permissions de `/v1/apps/state`.
- **Acceptance** : app/permission/status + policies globales lisibles.

### P7 — Storage
- Front `/v1/storage` ; actions PR.
- Go : `/v1/changes/storage-class` (édite `config/platform.nix`) +
  `/v1/changes/app-storage` (class d'un volume dans `apps/<app>.nix`) ;
  étendre `relPathOK` à `config/platform.nix`.
- **Acceptance** : volume critique non protégé signalé ; PR générées.

### P8 — Changes (lifecycle)
- Go : `/v1/changes/refresh` (`gh pr view <n> --json state,mergeable,
  statusCheckRollup,reviewDecision`).
- Front : CI/review/merge/deploy/risk + déclencher deploy si mergé.
- **Acceptance** : statut PR live ; deploy déclenchable.

### P9 — Library / Workshop (gros morceau)
- Go : `/v1/library/catalog/<id>` (fetch manifest catalogue GitHub@ref :
  modules + permissions/secrets/volumes/ports/risque) ;
  `/v1/library/module/<id>/preview`.
- Front : browse, filtres, « ce module demande … », choisir storage class,
  remplir params, preview, installer (`/v1/changes/app-install`).
- **Acceptance** : install reproductible (SHA exact, lock) ; perms affichées
  avant Install.

### P10 — Settings (config plateforme)
- Go : change types `config/*` (`platform-config`, `policy-config`,
  `catalog-add`, `access-role`) ; `relPathOK` → `config/`. Rôle admin.
- Front : update policies, catalogs, default storage, backup backend, access
  roles, security defaults.
- **Acceptance** : édition = PR ; rôle admin requis.

### P11 — Monitoring (réduit) + System (finition)
- Monitoring : health apps, apps down dérivées, audit récent, `/v1/system`,
  logs rapides. Pas d'alerting Prometheus.
- System : génération, commit déployé/main/behind, services infra, rollback
  génération, Tailscale/SSH/Docker status.
- **Acceptance** : pas de dépendance Grafana ; rollback OS toujours OK.

---

## 4. Backend deltas (control-api) — signatures

| Endpoint | Méthode | Rôle min | Fichier | Détail |
| --- | --- | --- | --- | --- |
| `/v1/me` | GET | viewer | `me.go` | `{ email, role }` depuis headers oauth2-proxy + `access.json`. |
| `/v1/system` | GET | viewer | `system.go` | cpu%, mem%, disk%, load, uptime, generation, commit, behind_main, infra services. Stdlib `/proc` + `syscall.Statfs`. |
| `/v1/logs` | GET | operator | `logs.go` | `?app=` → `journalctl -u app-<app> -n 200`. Valide nom. |
| `/v1/library/catalog/{id}` | GET | viewer | `library.go` | clone/fetch catalogue GitHub@ref (lecture seule, cache), parse manifest. |
| `/v1/changes/refresh` | POST | operator | `change_ext.go` | `gh pr view` live status, met à jour les records. |
| `/v1/changes/storage-class` | POST | maintainer | `change_ext.go` | PR édite `config/platform.nix`. |
| `/v1/changes/app-storage` | POST | operator | `change_ext.go` | PR change la class d'un volume. |
| `/v1/changes/platform-config` | POST | admin | `change_ext.go` | PR `config/platform.nix`. |
| `/v1/changes/policy-config` | POST | admin | `change_ext.go` | PR `config/policies.nix`. |
| `/v1/changes/catalog-add` | POST | admin | `change_ext.go` | PR `config/catalogs.nix`. |
| `/v1/changes/access-role` | POST | admin | `change_ext.go` | PR `config/access.json`. |

Modifs transverses :
- `authz.go` : `actorFromRequest` lit `X-Forwarded-Email` ; `requireMutationAuth`
  exige une identité oauth2-proxy (retire token UI + bypass loopback-Grafana).
- `relPathOK` : autoriser `config/platform.nix`, `config/policies.nix`,
  `config/catalogs.nix`, `config/access.json`.
- `main.go` : file-server SPA (sert `WEB_ROOT`, fallback `index.html`),
  ordre mux API d'abord. Retirer routes grafana/kiosk/ui.
- `CONTROL_API_ADDR` → `127.0.0.1:9092`.

Suppressions : `ui.go`, `grafanaStatHandler`, gauges/alertes dans
`metricsHandler` (garder `/metrics` minimal ou retirer), `promQuery`.

---

## 5. Frontend `web/` (React + Vite + shadcn/ui)

```
web/
  package.json            # vite, react, @tanstack/react-query, shadcn deps
  vite.config.ts          # base "/", build → dist
  index.html
  src/
    main.tsx
    api/
      client.ts           # fetch wrapper, gère 401→redirect oauth2-proxy login
      hooks.ts            # useApps(), usePolicies(), useBackups(), useMe()...
    components/
      StateBadge.tsx      # desired | runtime | action | risk
      ActionButton.tsx    # variante "Proposer (PR)" vs "Exécuter (audité)"
      DataTable.tsx       # wrapper shadcn table
      ConfirmDialog.tsx   # double-confirm runtime
      RoleGate.tsx        # masque selon /v1/me.role (sécu reste serveur)
    layout/
      AppShell.tsx        # nav latérale + topbar identité
      Nav.tsx             # Overview, Apps, Library, Changes, Storage, Secrets,
                          # Backups, Security, Monitoring, System, Settings
    screens/
      Overview.tsx  Apps.tsx  Library.tsx  Changes.tsx  Storage.tsx
      Secrets.tsx   Backups.tsx  Security.tsx  Monitoring.tsx  System.tsx
      Settings.tsx
```

Principes :
- TanStack Query : poll/refetch, pas de logique métier client (rendu + appels).
- `ActionButton` impose le choix : **PR** (durable) ou **Runtime** (audité).
- `StateBadge` rend les 4 états de façon homogène sur tous les écrans.
- `RoleGate` masque les actions hors-rôle (confort UX ; l'autorisation réelle
  est serveur — un bouton masqué reste refusé côté API).
- Secrets : aucun champ valeur en lecture ; formulaire d'écriture → POST chiffré.

---

## 6. NixOS — modules

### `modules/auth.nix` (oauth2-proxy + tailscale serve)
```nix
{ config, lib, pkgs, ... }:
{
  services.oauth2-proxy = {
    enable = true;
    provider = "github";
    clientID = "$(cat /run/secrets/oauth2_client_id)";   # via keyFile / env
    keyFile = "/run/secrets/oauth2_proxy_env";            # CLIENT_SECRET + COOKIE_SECRET
    httpAddress = "127.0.0.1:4180";
    reverseProxy = true;
    setXauthrequest = true;
    upstream = [ "http://127.0.0.1:9092" ];
    email.domains = [ "*" ];
    extraConfig = {
      "pass-user-headers" = "true";
      "set-xauthrequest" = "true";
      # restreindre : github-org / github-team
    };
  };

  # Expose oauth2-proxy sur le tailnet en HTTPS.
  systemd.services.tailscale-serve = {
    after = [ "tailscaled.service" "oauth2-proxy.service" ];
    wantedBy = [ "multi-user.target" ];
    serviceConfig.Type = "oneshot";
    serviceConfig.RemainAfterExit = true;
    script = "${pkgs.tailscale}/bin/tailscale serve --bg --https=443 http://127.0.0.1:4180";
  };
}
```
(clientID/secret/cookie via SOPS ; valeurs exactes à cadrer en P0.)

### `modules/control-api.nix` (modifs)
- `CONTROL_API_ADDR = "127.0.0.1:9092"` (loopback).
- `WEB_ROOT = "${webPkg}"` (bundle React).
- Retirer l'ouverture firewall `tailscale0` du port 9092.
- `webPkg = pkgs.buildNpmPackage { pname = "homelab-ui"; src = ../web;
  npmDepsHash = "..."; installPhase = "cp -r dist $out"; };`

### Retraits
- `hosts/homelab/configuration.nix` : retirer `../../modules/monitoring.nix`,
  `../../modules/observability.nix`. Supprimer ces fichiers + `modules/dashboards/`.
- Garder `modules/backup.nix`, `modules/platform.nix`, apps, secrets, tailscale.

---

## 7. Secrets (SOPS) à provisionner

| Secret | Usage |
| --- | --- |
| `oauth2_client_id` | GitHub OAuth app client id. |
| `oauth2_proxy_env` | `OAUTH2_PROXY_CLIENT_SECRET` + `OAUTH2_PROXY_COOKIE_SECRET`. |
| `restic_password` | (déjà prévu) si backups activés. |

Déclarés dans `secrets/homelab.yaml` (système), chiffrés age. Jamais en clair.

---

## 8. CI/CD

- `.github/workflows/checks.yml` :
  - Job `web` : `npm ci && npm run build` (typecheck + build Vite).
  - Job `go` : inchangé (vet/test/gofmt) — vérifier que `/v1/system` reste
    stdlib (pas de nouvelle dép → `vendorHash = null` tient).
  - `nix flake check` : doit passer sans monitoring/observability ; ajouter
    `webPkg` au flake (build npm reproductible, `npmDepsHash`).
- `deploy.yml` : le filtre `changes` doit inclure `web/**` et `modules/auth.nix`.

---

## 9. Plan de test

- **Auth** : non-authentifié → 302 login oauth2-proxy, aucun `/v1/*` atteint ;
  rôle insuffisant → 403 serveur même bouton masqué ; header `X-Forwarded-Email`
  forgé depuis l'extérieur → impossible (loopback only).
- **Go** : `/v1/me`, `/v1/system` (parsing `/proc` mocké), `/v1/logs` (regex
  unit), library catalog (fetch mocké), changes refresh, storage/settings PR.
- **Séparation** : aucune action runtime ne commit ; aucune PR n'exécute.
- **Secrets** : l'UI ne reçoit jamais de valeur (assert no `value`).
- **Front** : chaque écran rend sur payload vide + peuplé ; `npm run build` vert.
- **Nix** : `nix flake check` vert post-teardown ; `webPkg` build reproductible.
- **Runtime** : accès via MagicDNS HTTPS ; rollback OS toujours OK ; restart app
  audité.

---

## 10. Migration & rollback

Ordre impératif (ne jamais perdre l'accès) :
1. P0 d'abord : oauth2-proxy + tailscale serve + serving SPA **pendant que
   Grafana tourne encore**. Vérifier l'accès à la nouvelle UI.
2. Seulement ensuite P1 (teardown Grafana/Prom/Loki).
3. Écrans P2→P11 incrémentaux ; chaque merge est déployable.

Rollback : rollback OS par génération NixOS reste la sécurité ultime. Si P1
casse l'accès, revert du commit teardown → Grafana revient (tant que non
supprimé du repo ; garder le commit de suppression isolé et facilement
revert-able).

---

## 11. Risques / points ouverts

- **oauth2-proxy** : créer l'app GitHub OAuth (callback `https://<magicdns>/oauth2/callback`),
  restreindre org/team, cookie secret 32 bytes. À cadrer en P0.
- **tailscale serve** : nécessite Tailscale ≥ récent + HTTPS activé (MagicDNS +
  certs). Vérifier la conf du tailnet.
- **Perte d'historique** : retirer Prometheus/Loki supprime métriques/logs
  historiques. Monitoring devient « live only ». Réversible.
- **Métriques système stdlib** : `/proc`+statfs suffisent pour cpu/mem/disk/load ;
  pas de per-container (cAdvisor parti) — acceptable pour un control plane.
- **buildNpmPackage** : `npmDepsHash` à régénérer à chaque changement de
  `package-lock.json`.
```
