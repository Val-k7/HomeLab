# Plan d'implémentation — HomeLab Platform V2

Plan d'exécution concret pour finir la migration « Platform V2 ». Chaque item
cite les fichiers réels à toucher, la donnée à produire, et un critère
d'acceptation testable. L'ordre respecte les dépendances : on ne livre pas une
phase tant que ses prérequis ne sont pas verts.

État de départ (audit du 2026-06-08) : le dépôt est au stade « V2 GitOps »
(cf. `docs/archive/V2-PLAN.md`). Le modèle d'app est v1 (`{ runner, image/tag,
repo/rev, dir, port, metrics }`, auto-découvert par `modules/apps.nix`). RBAC
(`control-api/authz.go`), rollback OS+app par PR (`change_gateway.go`), socle
SOPS 1-secret (`modules/secrets.nix`) et UI changes/deploy existent. Tout le
reste du plan Platform V2 est à construire.

Principe inchangé : **un seul chemin d'écriture = git**. Changement durable → PR.
Opération runtime → action locale auditée. Aucune mutation hors-bande de l'hôte.

---

## Vue d'ensemble des workstreams

```
W0 Fondations config        ─┐
W1 App Model V2 (normalize)  ├─> prérequis durs de tout le reste
W2 Reproductibilité/updates ─┘
        │
        ├─> W3 Storage classes
        │       └─> W6 Backups/Restore
        ├─> W4 Secrets par app (SOPS)
        ├─> W5 Policy engine ──> W8 CI gates
        ├─> W7 Workshop / library
        │
        └─> W9 API enrichie ──> W10 UI control plane ──> W11 Monitoring/alertes
                                                              └─> W12 Migration apps
```

Jalons livrables (chacun laisse le système déployable) :

- **M1** — Config globale + normalisation v1/v2 (W0+W1+W2), zéro changement runtime.
- **M2** — Storage + Secrets par app + Policy engine en warning (W3+W4+W5).
- **M3** — Backups/Restore + API/UI enrichies sur desired/runtime (W6+W9+W10).
- **M4** — Workshop complet + CI stricte + migration apps restantes (W7+W8+W12).

---

## W0 — Fondations config

**Objectif** : introduire la configuration globale déclarative, lue par Nix et exposée à l'API en read-only.

### W0.1 `config/platform.nix`
- Nouveau fichier. Attribut Nix pur (pas d'IO), importé par un nouveau module `modules/platform.nix`.
- Champs : `host` (hostname, timezone, locale), `network` (trustedInterfaces, tailnet), `storageClasses` (map nom→{type, basePath, backedUp}), `backup` (backend, repo, schedule), `updatePolicyDefault`, `observability` (defaults rétention), `paths` (dataRoot, secretsRoot), `defaultVisibility` (`private`).
- `modules/platform.nix` : valide via `lib.types` (option set), expose `config.platform.*`, et écrit `environment.etc."homelab/platform.json".text = toJSON platform;` pour consommation API.
- **Acceptance** : `nix eval .#nixosConfigurations.homelab.config.platform` OK ; `platform.json` généré ; valeur inconnue de `storageClasses` rejetée par le type.

### W0.2 `config/policies.nix`
- Map déclarative consommée par W5/W8. Champs : `forbidden` (privileged, hostRootMount, dockerSocket, secretInline), `image` (`requireDigest` bool, `allowLatest` bool), `secrets` (`allowInline=false`), `backupByCriticality` (`{ high=required; critical=required+restoreTest; }`), `ports` (`allowPublic=false`, reserved), `update` (`automergeAllowed` set).
- Exposé via `environment.etc."homelab/policies.json"`.
- **Acceptance** : fichier importable, JSON généré, schéma figé documenté dans `docs/configuration.md`.

### W0.3 `config/catalogs.nix`
- Liste catalogues workshop : `{ id, repo, ref (tag/sha), trust (official|community|untrusted), policy }`.
- Exposé via `environment.etc."homelab/catalogs.json"`. Vide par défaut.
- **Acceptance** : JSON généré, liste vide valide.

### W0.4 Câblage
- `hosts/homelab/configuration.nix` : ajouter `../../modules/platform.nix` aux imports.
- Garder `.env` pour bootstrap (clés SSH, tokens) — ne PAS y migrer la config plateforme.
- **Acceptance** : `nix flake check --impure --no-build` vert.

---

## W1 — App Model V2 + normalisation

**Objectif** : modèle interne unique, v1 et v2 normalisés vers la même structure, zéro changement de comportement runtime à ce stade.

### W1.1 Normaliseur Nix `lib/app-model.nix`
- Fonction `normalize : rawApp -> normalizedApp`.
- Détecte `schemaVersion`. Absent ou `1` → mappe v1 (`runner/image/tag/repo/rev/dir/port/metrics`) vers le modèle interne avec defaults (`criticality="low"`, `permissions=[]`, `volumes=[]`, `secrets=[]`, `updatePolicy=platform.updatePolicyDefault`, `healthcheck=null`).
- `schemaVersion=2` → lit les champs complets : `runtime{runner,image,tag,digest,repo,rev,hash,ports}`, `source`, `updatePolicy`, `criticality`, `permissions`, `volumes`, `secrets`, `healthcheck`, `metrics`, `dependencies`.
- **Acceptance** : `normalize` d'un app v1 == sortie attendue ; tests Nix (`tests/app-model.nix`) couvrant v1 et v2.

### W1.2 Refactor `modules/apps.nix`
- Remplacer l'import direct par `normalize` sur chaque entrée.
- `mkService` consomme le modèle normalisé (pas les champs bruts). Les 4 runners existants (`image/process/compose/dockerfile`) gardent leur comportement.
- `manifest` enrichi : ajouter `schemaVersion`, `criticality`, `permissions`, `volumes`, `updatePolicy`, `healthcheck`, `source`. **Ne jamais inclure de valeur de secret** (juste les noms attendus).
- **Acceptance** : `whoami` (v1) déploie identiquement ; `apps.json` contient les nouveaux champs ; aucun secret en clair dans le manifest.

### W1.3 Runner v2 `nixos`
- Ajouter `nixosService` : app fournissant un attribut module NixOS (préféré quand dispo).
- `mkService` : router `nixos` → `nixosService`.
- **Acceptance** : app exemple `nixos` (option set) démarre via systemd natif.

---

## W2 — Reproductibilité et updates

**Objectif** : versions exactes uniquement en runtime ; intentions d'update séparées.

### W2.1 Champs version exacts
- Modèle v2 : `digest` (image), `hash` (Nix), `rev` (commit SHA) obligatoires en mode strict. `tag` devient une *intention*, pas la version déployée.
- `imageService` (déjà OK sur `sha256:`) : exiger `digest` si `policies.image.requireDigest`.
- **Acceptance** : test Nix « image sans digest refusée en mode strict ».

### W2.2 `updatePolicy`
- Trois valeurs : `autoLow` (PR auto + automerge si stateless+healthy), `manual` (PR auto, merge manuel), `critical` (PR manuelle, backup requis, restore test requis).
- Stocké dans le manifest, consommé par Renovate config + policy engine.
- **Acceptance** : champ présent dans `apps.json`, valeur inconnue rejetée.

### W2.3 Renovate
- `renovate.json` : ajouter `pinDigests: true`, packageRules ciblant `apps/*.nix` (regex managers pour images `image=`/`tag=` et refs git `rev=`).
- Automerge conditionnel sur `updatePolicy=autoLow` uniquement.
- Jamais de push direct serveur (déjà le cas, PR-only).
- **Acceptance** : `renovate-config-validator` vert ; PR de test propose digest exact.

---

## W3 — Storage et données

**Objectif** : chemins résolus depuis storage classes ; données hors Git.

### W3.1 Résolveur de chemins `lib/storage.nix`
- `resolve : {class, app, volume} -> path`. Ex : `nas + jellyfin + config -> /mnt/homelab/jellyfin/config` (base depuis `platform.storageClasses.<class>.basePath`).
- Classes : `local` (défaut), `nas` (NFS), `fast` (SSD), `cache` (non sauvegardé, `backedUp=false`).
- **Acceptance** : test « class inconnue refusée » ; résolution déterministe.

### W3.2 Volumes logiques dans le modèle
- App déclare `volumes = [{ name, kind (config|data|database|media|cache), class }]`.
- `modules/apps.nix` : monter les volumes résolus dans les services (bind/`ReadWritePaths` ou `-v` docker).
- **Acceptance** : app exemple monte `data` sur le bon chemin ; `cache` marqué non-sauvegardé.

### W3.3 Garde-fous
- Volumes `database` → bloquent automerge sauf policy explicite (consommé W5).
- `.gitignore` : confirmer que `dataRoot` n'est jamais commitable.
- **Acceptance** : test policy « database bloque automerge ».

---

## W4 — Secrets par app (SOPS/age)

**Objectif** : secrets chiffrés par app, statut exposé sans fuite.

### W4.1 Convention fichiers
- `secrets/apps/<app>.yaml` (SOPS). `.sops.yaml` : ajouter règle de chiffrement pour `secrets/apps/.*`.
- `modules/secrets.nix` : auto-découvrir `secrets/apps/*.yaml`, déclarer `sops.secrets` avec owner/group `controlapi` ou l'app.
- **Acceptance** : `secrets/apps/whoami.yaml` chiffré déchiffrable sur l'hôte ; clé age hors Git (déjà le cas).

### W4.2 Déclaration côté modèle
- Modèle v2 : `secrets = [{ name, required (bool), mountPath }]`. Le module communautaire (W7) déclare les secrets attendus.
- **Refus** des secrets inline dans `apps/*.nix` (validé W5).
- **Acceptance** : app déclare un secret, monté en `/run/secrets/...`.

### W4.3 Statut sans fuite
- Nouveau `control-api/secrets.go` : `/v1/secrets/status` → par app/secret : `present | missing | optional_missing`. Lit la *présence* du fichier/clé SOPS, **jamais la valeur**.
- **Acceptance** : test Go « status ne contient aucune valeur » ; missing/optional détectés.

### W4.4 Création via UI → PR
- Endpoint change : `/v1/changes/app-secret` qui produit un fichier SOPS chiffré (appel `sops --encrypt` côté serveur avec la clé publique age) et ouvre une PR. Jamais de valeur en clair dans le manifest ou l'audit.
- **Acceptance** : PR générée contient uniquement du contenu chiffré.

---

## W5 — Policy engine

**Objectif** : validateur partagé CI + control-api, refus par défaut.

### W5.1 Validateur `control-api/policy_engine.go`
- Fonction pure `Validate(app NormalizedApp, policies Policies) []Violation`.
- Refus par défaut : privileged, montage `/`, `/run/secrets` non déclaré, Docker socket, port public, secret inline, image sans digest (strict), git ref non-SHA (strict).
- Permissions explicites : `docker`, `tailnet-port`, `public-port`, `persistent-storage`, `secret-access`, `metrics`, `privileged-container`, `host-root-mount`, `docker-socket`.
- **Acceptance** : tests Go par règle (chaque refus + chaque autorisation explicite).

### W5.2 CLI de validation `tools/validate-platform.go`
- Lit `apps.json` (ou évalue les `apps/*.nix`), `policies.json`, applique `Validate`. Mode `--warn` (M2) puis `--strict` (M4).
- **Acceptance** : binaire exécutable en CI ; sortie non-zero sur violation en strict.

### W5.3 Rôles (déjà partiels)
- `authz.go` couvre déjà viewer/operator/maintainer/admin. Mapper les nouveaux endpoints W9 sur le bon `minRole` (deploy/reboot=admin, rollback=maintainer, PR non critiques=operator, lecture=viewer).
- **Acceptance** : test Go « endpoint backup-now exige maintainer ».

---

## W6 — Backups et restore

**Objectif** : jobs backup déclaratifs depuis les volumes ; actions runtime auditées.

### W6.1 Backend `modules/backup.nix`
- restic par défaut (repo + schedule depuis `platform.backup`). Générer un `systemd.services`/`timers` par volume `backedUp`.
- **Acceptance** : timer restic créé pour les volumes `config`/`data`/`database` ; `cache` exclu.

### W6.2 État backup côté API
- `control-api/backups.go` : `/v1/backups` (par app : dernier backup, dernier restore test, taille, durée, erreur, couverture).
- **Acceptance** : test Go lecture statut depuis sortie restic (mock).

### W6.3 Actions runtime
- Endpoints : `/v1/backups/run` (backup now), `/v1/backups/restore-test`, `/v1/backups/verify`, `/v1/backups/snapshots`, `/v1/backups/restore` (vers chemin temporaire). Tous audités, `minRole=maintainer`.
- **Acceptance** : actions appellent restic, écrivent un audit event, ne touchent jamais Git.

### W6.4 Gates policy
- App `high`/`critical` sans volume sauvegardé → refus (W5). App `critical` sans restore test récent → warning puis refus selon policy.
- **Acceptance** : test « critical sans backup refusé ».

---

## W7 — Workshop / library

**Objectif** : catalogue communautaire, installation verrouillée par version exacte.

### W7.1 Format catalogue
- Schéma module : `{ id, name, description, repo, path, version, runners, secrets, volumes, permissions, healthchecks, metrics, defaultCriticality }`. Documenté `docs/`.
- **Acceptance** : exemple de catalogue parsé et validé.

### W7.2 `workshop-lock.json`
- Racine du repo. Par module installé : `{ module, catalog, version, repo, sha, hash }`.
- **Acceptance** : schéma figé ; un module sans entrée lock → refus d'exécution (W5/W8).

### W7.3 Flux d'installation
- `/v1/library` (read-only, liste catalogues activés via `catalogs.json`). `/v1/changes/app-install` : génère `apps/<app>.nix` (schemaVersion=2), entrée `workshop-lock.json`, fichiers SOPS chiffrés éventuels, **aucune donnée applicative**, puis PR.
- **Acceptance** : PR d'install reproductible, lock cohérent, manifest sans secret.

### W7.4 Garde anti-exécution non verrouillée
- Policy : un module communautaire non présent dans `workshop-lock.json` est refusé.
- **Acceptance** : test « module non locké refusé ».

---

## W8 — CI/CD gates

**Objectif** : étendre la CI avec la validation plateforme.

### W8.1 Garder l'existant
- Go test/vet, ShellCheck, `nix flake check --impure --no-build`, dashboard JSON, PR-first guards (`deploy.yml`).

### W8.2 Nouveaux checks (`.github/workflows`)
- `config/platform.nix` + `config/policies.nix` valides (eval).
- storage class connue ; update policy connue ; workshop-lock valide ; schema app v1/v2 valide ; secrets requis déclarés ; app critique couverte backup ; ports non conflictuels ; permissions interdites refusées ; `latest` interdit en mode strict.
- Câbler `tools/validate-platform.go` (warn en M2, strict en M4 pour les nouvelles apps).
- **Acceptance** : CI rouge sur app non conforme en strict ; verte sur apps v1 existantes.

---

## W9 — Control API enrichie

**Objectif** : exposer desired/runtime/drift et les nouveaux domaines.

### W9.1 `/v1/apps` enrichi
- Par app : `desired` (depuis Git/manifest), `runtime` (état serveur), `drift`, `storage`, `secrets status`, `backup status`, `policy status`, `update status`.
- **Acceptance** : test Go « /v1/apps expose drift, aucun secret ».

### W9.2 Endpoints read-only
- `/v1/platform`, `/v1/policies`, `/v1/storage`, `/v1/library`, `/v1/secrets/status` (lisent les `*.json` de `/etc/homelab/`).
- **Acceptance** : chaque endpoint répond, `viewer` autorisé.

### W9.3 Endpoints PR/change
- `app-install` (W7), `app-update` (existe), `app-rollback` (existe), `storage-change`, `policy-change`, `app-secret` (W4), `permissions-change`. Tous → PR via change gateway.
- **Acceptance** : chaque endpoint produit une PR, jamais de mutation directe.

### W9.4 Endpoints runtime
- `backup-now`, `restore-test`, `healthcheck-now`, `logs`, `runtime-drift-refresh`. Audités, rôle adéquat.
- **Acceptance** : actions auditées, séparées des PR.

---

## W10 — UI control plane

**Objectif** : UI plateforme, pas seulement panneau d'actions.

### W10.1 Sections
- Overview, Apps, Library/Workshop, Changes/PRs, Storage, Secrets, Backups, Security, Monitoring, System, Settings.
- Chaque écran distingue : desired (Git) / runtime (serveur) / drift / open changes / policy status.

### W10.2 Deux types d'action
- Changement durable → bouton « Proposer (PR) ». Opération runtime → bouton « Exécuter (audité) ». Visuellement distincts.
- **Acceptance** : tests UI (existants dans `main_test.go` style) ; aucune action runtime ne crée de commit, aucune PR n'exécute directement.

---

## W11 — Monitoring, metrics, health

**Objectif** : dashboards desired-vs-runtime + alertes.

### W11.1 Healthchecks
- Obligatoires pour toute app v2 (timeout configurable). Consommés par `/v1/health/apps` et comme gate post-déploiement pour apps importantes.
- **Acceptance** : déploiement bloqué si healthcheck KO sur app importante.

### W11.2 Dashboards (`modules/dashboards/`)
- Ajouter panels : desired vs runtime, health apps, backup status, storage usage, secrets missing, PR/update status, deploy status, drift Git/runtime, policy violations.
- **Acceptance** : JSON valides (check CI existant) ; panels alimentés par les nouveaux endpoints/metrics.

### W11.3 Alertes
- app down, backup failed, disk nearly full, secret missing, update PR failing, deploy pending, app drift.
- **Acceptance** : règles Prometheus/Grafana provisionnées.

---

## W12 — Migration

**Objectif** : basculer le réel sans rupture.

1. W0 livré : fichiers config globaux avec defaults.
2. W1 livré : normalisation v1/v2, comportement runtime inchangé.
3. Migrer `whoami` en v2 (app exemple).
4. Ajouter storage classes + manifest enrichi sur `whoami`.
5. Ajouter secret SOPS par app sur `whoami`.
6. Policy validator en warning.
7. Brancher UI/API sur desired/runtime enrichi.
8. CI stricte pour **nouvelles** apps uniquement.
9. Migrer les apps existantes restantes.
10. Workshop complet.

---

## Plan de test (gate de chaque jalon)

**Nix** : v1 existant valide ; v2 image valide ; storage class inconnue refusée ;
update policy inconnue refusée ; app critique sans backup refusée ; secret inline
refusé ; manifest enrichi généré.

**Go** : `go test ./...`, `go vet ./...` ; tests v1/v2 ; desired/runtime ; secrets
status (zéro fuite) ; backup status ; policy violations ; PR generation.

**CI** : `nix flake check --impure --no-build` ; dashboard JSON ; ShellCheck ;
validate platform ; validate workshop lock ; validate no runtime `latest`.

**Runtime** : app exemple démarre ; healthcheck OK ; `/v1/apps` ne fuit aucun
secret ; backup status visible ; rollback système OK ; restart app audité.

---

## Hypothèses

Migration progressive, pas de rupture. SOPS/age = backend secret principal.
GitHub Secrets non utilisé comme coffre. Git = config durable + secrets chiffrés,
jamais les données. Storage local par défaut. Update policy `manual` par défaut.
Workshop après le socle platform/app v2. UI/API séparent toujours changement Git
et action runtime.
