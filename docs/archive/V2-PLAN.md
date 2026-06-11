# V2 — Plan d'implémentation

Plan complet de migration du homelab NixOS vers l'architecture V2 : GitOps pur,
plus aucun script de déploiement local, apps déclaratives auto-découvertes,
mises à jour automatiques par PR, monitoring read-only.

Ce document est le seul plan de référence. Il est découpé en phases livrables
indépendamment. Chaque phase laisse le système dans un état déployable.

---

## 1. Objectifs et principes

**Objectif** : un seul chemin d'écriture — `git`. Tout déploiement, mise à jour,
rollback et ajout de service passe par un commit. Aucune action manuelle sur
l'hôte en fonctionnement normal.

**Principes directeurs**

1. **Git = source unique de vérité.** Aucune mutation hors-bande de l'hôte.
2. **Déclaratif d'abord.** Source des projets épinglée (`rev`). Rollback via
   les générations NixOS.
3. **Zéro script plateforme.** Plus de `.ps1`. Le repo ne contient que du Nix,
   du YAML et du Markdown.
4. **Pas de SSH public.** Accès uniquement via le tailnet.
5. **Friction basse pour ajouter un service.** Déposer un fichier dans `apps/`,
   pousser, c'est tout.
6. **Build-vs-buy.** Monitoring et détection de mises à jour = outils existants
   (Beszel, Renovate), pas de service maison.

---

## 2. Architecture cible

```
homelab-dev/
├── flake.nix
├── lib/
│   ├── load-env.nix
│   └── env-lib.nix
├── hosts/
│   └── homelab/
│       ├── configuration.nix
│       └── hardware-configuration.nix
├── modules/
│   ├── networking.nix
│   ├── ssh.nix
│   ├── docker.nix
│   ├── tailscale.nix
│   └── apps.nix              # nouveau : moteur d'apps auto-scan
├── apps/                     # nouveau : une déclaration par service
│   ├── immich.nix
│   ├── mon-api.nix
│   └── beszel.nix
├── secrets/                  # phase 4 : sops-nix
│   └── homelab.yaml
├── .github/
│   └── workflows/
│       ├── deploy.yml        # refonte : validation avant switch
│       └── rollback.yml      # nouveau : workflow_dispatch(generation)
├── renovate.json             # nouveau : watch upstream → PR
├── CLAUDE.md
├── AGENTS.md
└── V2-PLAN.md
```

**Flux complet**

```
écrire apps/<nom>.nix  ─push─▶  CI deploy.yml
                                  ├─ runner: nix flake check (.env.example)
                                  └─ host:   nixos-rebuild build → switch (over tailscale)
                                                    │
Renovate ─watch upstream─▶ PR "bump rev" ──merge──┘

rollback : Actions ▶ rollback.yml ▶ choisir génération
break-glass : Tailscale SSH
monitoring : Beszel (app) sur le tailnet, read-only
```

---

## 3. Suppressions

À retirer du repo dès la phase 1.

| Fichier | Raison |
|---|---|
| `homelab.ps1` | TUI Windows, non fiable, lié plateforme. Remplacé par CI + Beszel + Tailscale SSH. |
| `setup-ssh.ps1` | Générait la clé workstation pour SSH manuel. Inutile : SSH manuel = Tailscale SSH (pas de fichier clé). |
| `var/` (local) | Artefacts d'un autre projet (bot Polymarket). Déjà gitignored ; supprimer le dossier local. |

Après suppression : plus aucun `.ps1` dans le repo.

**Note `.gitignore`** : la ligne `.gitignore` qui s'auto-ignore est à vérifier
(probablement une erreur). À confirmer puis corriger.

---

## 4. Phase 1 — Durcissement réseau et nettoyage

But : fermer le SSH public, supprimer les scripts, sans toucher au modèle de
déploiement existant. Le système reste déployable par CI tel quel.

### 4.1 SSH uniquement sur le tailnet

`modules/networking.nix` — retirer `SSH_PORT` des ports ouverts globalement.
Le firewall n'expose plus rien sur WAN/LAN à part ce qui est explicitement
déclaré. SSH devient joignable uniquement via `tailscale0`, qui est déjà
`trustedInterface` (le trafic d'une interface de confiance contourne le
firewall).

État cible `networking.firewall` :

```nix
firewall = {
  enable = true;
  allowedTCPPorts = [ ];
};
```

`modules/tailscale.nix` conserve `trustedInterfaces = [ "tailscale0" ]`.
SSH reste donc accessible sur l'IP tailscale de l'hôte, et nulle part ailleurs.

### 4.2 Tailscale SSH comme break-glass

Activer Tailscale SSH (ACL-gated, pas de clé sur disque) en complément
d'OpenSSH :

```nix
services.tailscale.extraUpFlags = [ "--ssh" ];
```

Les règles d'accès sont gérées dans l'ACL du tailnet (console Tailscale), pas
dans le repo. C'est le filet de sécurité si la CI casse ou si une mauvaise
config rend OpenSSH inaccessible.

### 4.3 Adresse fixe tailscale

L'hôte doit avoir une IP tailscale stable (`100.x.y.z`) ou un nom MagicDNS.
C'est cette adresse qui devient `SSH_HOST` côté CI. À noter dans la doc de
provisioning (pas dans le repo).

### 4.4 Suppressions

- Supprimer `homelab.ps1`, `setup-ssh.ps1`.
- Supprimer le dossier local `var/`.

### 4.5 Validation phase 1

- `nixos-rebuild switch` réussit.
- Depuis le tailnet : `ssh homelab` fonctionne.
- Depuis l'extérieur du tailnet : le port SSH ne répond pas (`nmap`/`nc`).
- Tailscale SSH fonctionne (`tailscale ssh homelab`).

---

## 5. Phase 2 — CI robuste

But : CI seul chemin de déploiement, avec validation avant activation et un
chemin de rollback déclenchable depuis GitHub.

### 5.1 Combler le trou `.env`

`homelab.ps1` copiait `.env` par `scp`. La CI l'exclut. Sans elle, l'hôte doit
déjà posséder `.env`. Stratégie de transition (avant sops, phase 4) :

- **Valeurs non sensibles** (`HOSTNAME`, `INTERFACE`, IP statique, etc.) :
  laisser dans `.env` sur l'hôte, **bootstrapé une fois à la main** lors du
  provisioning.
- Documenter explicitement cette étape de bootstrap dans `AGENTS.md`/`CLAUDE.md`.
- Le secret tailscale reste un fichier hôte (`TAILSCALE_AUTHKEY_FILE`), inchangé.

> La dette `.env --impure` est éliminée en phase 4 (sops-nix). Phase 2 se
> contente de rendre l'état explicite et documenté.

### 5.2 Refonte `deploy.yml`

Deux étages de validation avant le `switch`.

```yaml
name: deploy

on:
  push:
    branches: [main]
  workflow_dispatch: {}

concurrency:
  group: deploy-homelab
  cancel-in-progress: false

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: cachix/install-nix-action@v27
      - name: Eval gate
        run: |
          cp .env.example .env
          nix flake check --impure --no-build

  deploy:
    needs: check
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Connect to tailnet
        uses: tailscale/github-action@v3
        with:
          oauth-client-id: ${{ secrets.TS_OAUTH_CLIENT_ID }}
          oauth-secret: ${{ secrets.TS_OAUTH_SECRET }}
          tags: tag:ci
      - name: Load deploy key
        run: |
          mkdir -p ~/.ssh
          printf '%s\n' "${{ secrets.SSH_KEY }}" > ~/.ssh/id_ci
          chmod 600 ~/.ssh/id_ci
      - name: Sync repo to host
        run: |
          rsync -az --delete \
            --exclude '.git' --exclude 'var' --exclude 'result' \
            --exclude 'result-*' --exclude '.env' \
            -e "ssh -i ~/.ssh/id_ci -o StrictHostKeyChecking=accept-new" \
            ./ "${{ secrets.SSH_USER }}@${{ secrets.SSH_HOST }}:/home/${{ secrets.SSH_USER }}/homelab/"
      - name: Build gate
        run: |
          ssh -i ~/.ssh/id_ci -o StrictHostKeyChecking=accept-new \
            "${{ secrets.SSH_USER }}@${{ secrets.SSH_HOST }}" \
            "cd /home/${{ secrets.SSH_USER }}/homelab && sudo HOMELAB_ENV=\$PWD/.env nixos-rebuild build --flake .#homelab --impure"
      - name: Switch
        run: |
          ssh -i ~/.ssh/id_ci -o StrictHostKeyChecking=accept-new \
            "${{ secrets.SSH_USER }}@${{ secrets.SSH_HOST }}" \
            "cd /home/${{ secrets.SSH_USER }}/homelab && sudo HOMELAB_ENV=\$PWD/.env nixos-rebuild switch --flake .#homelab --impure"
```

- `check` : gate d'évaluation rapide sur runner avec `.env.example` (attrape les
  erreurs de syntaxe et de typage, pas les valeurs réelles).
- `Build gate` : compile la config réelle sur l'hôte **sans activer**. Un build
  qui échoue stoppe avant `switch` — l'hôte ne reçoit jamais une config
  cassée.
- `Switch` : active seulement après build réussi.

`SSH_HOST` = adresse fixe tailscale de l'hôte.

### 5.3 `rollback.yml`

```yaml
name: rollback

on:
  workflow_dispatch:
    inputs:
      generation:
        description: "Numéro de génération (vide = précédente)"
        required: false
        default: ""

jobs:
  rollback:
    runs-on: ubuntu-latest
    steps:
      - name: Connect to tailnet
        uses: tailscale/github-action@v3
        with:
          oauth-client-id: ${{ secrets.TS_OAUTH_CLIENT_ID }}
          oauth-secret: ${{ secrets.TS_OAUTH_SECRET }}
          tags: tag:ci
      - name: Load deploy key
        run: |
          mkdir -p ~/.ssh
          printf '%s\n' "${{ secrets.SSH_KEY }}" > ~/.ssh/id_ci
          chmod 600 ~/.ssh/id_ci
      - name: Rollback
        run: |
          GEN="${{ github.event.inputs.generation }}"
          if [ -z "$GEN" ]; then
            CMD="sudo nixos-rebuild switch --rollback"
          else
            CMD="sudo nix-env --profile /nix/var/nix/profiles/system --switch-generation $GEN && sudo /nix/var/nix/profiles/system/bin/switch-to-configuration switch"
          fi
          ssh -i ~/.ssh/id_ci -o StrictHostKeyChecking=accept-new \
            "${{ secrets.SSH_USER }}@${{ secrets.SSH_HOST }}" "$CMD"
```

Déclenchable depuis GitHub mobile. Couvre le rollback que faisait `homelab.ps1`.

### 5.4 Validation phase 2

- Un push qui casse l'eval est bloqué par `check`.
- Un push qui casse le build est bloqué par `Build gate` (l'hôte reste sur
  l'ancienne génération).
- Un push valide déploie.
- `rollback.yml` ramène à la génération choisie.

---

## 6. Phase 3 — Moteur d'apps

But : ajouter un service = déposer `apps/<nom>.nix` et pousser. Trois runners :
`compose`, `dockerfile`, `process`.

### 6.1 Auto-scan

`modules/apps.nix` lit `../../apps`, importe chaque `.nix`, et génère pour
chaque entrée une unité systemd (et l'ouverture de port si demandée).

```nix
{ config, lib, pkgs, ... }:

let
  appsDir = ../../apps;
  entries =
    if builtins.pathExists appsDir then
      lib.filterAttrs (n: t: t == "regular" && lib.hasSuffix ".nix" n)
        (builtins.readDir appsDir)
    else { };
  apps = lib.mapAttrs'
    (file: _:
      let name = lib.removeSuffix ".nix" file;
      in lib.nameValuePair name (import (appsDir + "/${file}")))
    entries;
in
{
}
```

Le corps construit `systemd.services` et `networking.firewall.allowedTCPPorts`
à partir de `apps`. Déposer un fichier = app active ; le supprimer = app retirée
au prochain switch.

> Décision verrouillée : **auto-scan** (pas de liste explicite).

### 6.2 Interface de déclaration

Champs communs :

| Champ | Type | Requis | Sens |
|---|---|---|---|
| `runner` | `"compose"` \| `"dockerfile"` \| `"process"` | oui | type d'exécution |
| `port` | int | non | port à ouvrir (tailnet seulement) |
| `env` | attrset | non | variables d'environnement (non sensibles) |
| `envFile` | path | non | fichier secret (sops, phase 4) |

Champs `process` / `dockerfile` (depuis git) :

| Champ | Sens |
|---|---|
| `repo` | URL git |
| `rev` | commit épinglé |

Champs `process` :

| Champ | Sens |
|---|---|
| `runtime` | paquet nixpkgs (`nodejs_22`, `python312`, …) |
| `buildCmd` | commande de build |
| `startCmd` | commande de démarrage |
| `packages` | paquets supplémentaires au PATH (optionnel) |

Champs `compose` :

| Champ | Sens |
|---|---|
| `dir` | chemin du dossier contenant `docker-compose.yml` |

### 6.3 Runner `process`

> Décision verrouillée : source épinglée par `rev`, **build impur à
> `ExecStartPre`**. Le build pur en dérivation Nix exigerait le hash des deps
> (node2nix/poetry2nix) — friction trop élevée pour un homelab. Conséquence
> assumée : source reproductible, résolution `npm`/`pip` non reproductible.

Unité générée pour `apps/mon-api.nix` :

```nix
{
  runner    = "process";
  repo      = "https://github.com/toi/mon-api";
  rev       = "a1b2c3d";
  runtime   = "nodejs_22";
  buildCmd  = "npm ci && npm run build";
  startCmd  = "node dist/index.js";
  port      = 3000;
  env       = { NODE_ENV = "production"; };
}
```

→ `systemd.services."app-mon-api"` :

```
Type=simple
StateDirectory=app-mon-api
WorkingDirectory=/var/lib/app-mon-api/src
DynamicUser=true
ExecStartPre=<script fetch+build>
ExecStart=<startCmd>
EnvironmentFile=-<envFile si défini>
Restart=on-failure
RestartSec=5

ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
NoNewPrivileges=true
ReadWritePaths=/var/lib/app-mon-api
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
```

PATH du service = `[ runtime git ] ++ packages`.

Script `ExecStartPre` (le `.built-rev` évite de rebuild si `rev` inchangé) :

```bash
if [ ! -d src/.git ]; then git clone "$REPO" src; fi
cd src
git fetch --depth 1 origin "$REV" && git checkout -q "$REV"
if [ "$(cat ../.built-rev 2>/dev/null)" != "$REV" ]; then
  sh -c "$BUILD_CMD"
  echo "$REV" > ../.built-rev
fi
```

Propriétés :

| Propriété | Comportement |
|---|---|
| Build échoue | le `switch` réussit (build en `preStart`, pas dans la dérivation) ; seule l'unité reste down et retry. Hôte non cassé. |
| Rollback | la génération revert la def d'unité + `rev` ; le mismatch `.built-rev` force un re-checkout. |
| Reproductibilité | source oui (`rev`), deps non (registry live). |
| Réseau | `port` ouvert sur `tailscale0` uniquement (interface de confiance). Pas d'expo WAN. |

### 6.4 Runners `compose` et `dockerfile`

Même fichier `apps/<nom>.nix`, même auto-scan.

`compose` :

```nix
{ runner = "compose"; dir = ./immich; }
```

→ unité `app-immich` : `ExecStart = docker compose -f <dir>/docker-compose.yml up`,
`ExecStop = docker compose ... down`.

`dockerfile` :

```nix
{
  runner = "dockerfile";
  repo   = "https://github.com/toi/projet";
  rev    = "f4e5d6c";
  port   = 8080;
}
```

→ `ExecStartPre` : `git fetch rev` + `docker build -t app-projet .` ;
`ExecStart` : `docker run --rm -p <port>:<port> app-projet`.

### 6.5 Validation phase 3

- Déposer une app `process` triviale (serveur HTTP), pousser → service actif,
  port joignable sur le tailnet.
- Bump `rev` → rebuild ; restart sans bump → pas de rebuild.
- Déposer une app `compose` (ex. `whoami`) → conteneur up.
- Supprimer un fichier `apps/*.nix` → service retiré au switch.

---

## 7. Phase 4 — Secrets (sops-nix)

But : éliminer le hack `.env --impure` et rendre l'hôte reproductible from
scratch, secrets inclus, le tout versionné chiffré.

- Ajouter `sops-nix` aux inputs du flake.
- `secrets/homelab.yaml` chiffré (age/GPG). Clé de déchiffrement = clé hôte
  (host SSH key convertie en age, ou clé age dédiée stockée hors repo).
- Migrer : authkey tailscale, `envFile` des apps, toute valeur sensible.
- Les valeurs **non sensibles** restent dans `.env` (ou migrent vers un attrset
  Nix committé par hôte) — à ce stade on peut viser la suppression de `--impure`
  en déplaçant la config non sensible dans le repo en clair.

Sortie de phase : les secrets vivent dans git (chiffrés), déchiffrés à
l'activation. Plus de fichier secret bootstrapé à la main.

---

## 8. Phase 5 — Mises à jour automatiques (Renovate)

But : détecter les nouveautés upstream et ouvrir des PR de bump. Aucun outil ne
mute l'hôte hors-bande (pas de Watchtower).

`renovate.json` couvre :

- **Inputs du flake** (`nixpkgs`, `sops-nix`) via le manager Nix.
- **Images conteneurs** des apps `compose`/`dockerfile` (tags/digests).
- **`rev` git des apps `process`** via un manager regex ciblant le champ `rev`.

Boucle :

```
upstream bouge → Renovate PR "bump rev/digest" → review/merge → CI deploy
```

> Watchtower est explicitement exclu : il tire et redémarre les conteneurs hors
> du contrôle de Nix, ce qui casse le modèle déclaratif et le rollback.

### Validation phase 5

- Une app épinglée sur un vieux `rev` génère une PR de bump.
- Merge de la PR → CI déploie la nouvelle version.

---

## 9. Phase 6 — Monitoring

But : visibilité read-only, cross-platform, sur le tailnet. Pas de WebUI maison.

- Déployer **Beszel** comme une app `compose` (`apps/beszel.nix`), exposée sur
  `tailscale0` uniquement.
- Accès navigateur (desktop + mobile) via le tailnet.
- Couvre les métriques système et conteneurs que montrait `homelab.ps1`.

Optionnel plus tard : un dashboard d'agrégation (Glance/Homepage) comme autre
app. Toujours read-only ; git reste le seul chemin d'écriture.

---

## 10. Ordre d'exécution et dépendances

```
Phase 1 (réseau + nettoyage)  ──▶ indépendante, à faire en premier
Phase 2 (CI robuste)          ──▶ dépend de 1 (SSH tailnet)
Phase 3 (moteur d'apps)       ──▶ dépend de 2 (CI valide les apps)
Phase 4 (secrets)             ──▶ dépend de 3 (envFile des apps)
Phase 5 (Renovate)            ──▶ dépend de 3 (revs à bumper)
Phase 6 (monitoring)          ──▶ dépend de 3 (Beszel = une app)
```

Phases 4, 5 et 6 sont parallélisables une fois la phase 3 livrée.

---

## 11. Risques et mitigations

| Risque | Mitigation |
|---|---|
| CI cassée → plus de déploiement | Tailscale SSH break-glass (phase 1). |
| ACL tailscale verrouille l'accès | Accès console/physique de dernier recours ; garder une règle ACL admin stable. |
| Build d'app casse l'activation | Build en `ExecStartPre`, isolé de l'unité ; le switch réussit. |
| Deps `npm`/`pip` non reproductibles | Assumé ; `rev` épingle la source ; envisager dream2nix si un service critique exige la reproductibilité totale. |
| Secret commité en clair par erreur | sops-nix (phase 4) + `.gitignore` strict ; revue des PR. |
| Renovate bump cassant auto-mergé | Pas d'auto-merge : toute PR passe par review + gates CI. |

---

## 12. Décisions verrouillées

- Suppression `homelab.ps1` + `setup-ssh.ps1` (zéro `.ps1`).
- SSH sur `tailscale0` uniquement + Tailscale SSH break-glass.
- Déploiement = GitOps (push `main`, validation avant switch).
- Rollback = `workflow_dispatch(generation)`.
- Apps = auto-scan `apps/*.nix`, runners `compose` / `dockerfile` / `process`.
- `process` = source épinglée, build impur en `ExecStartPre`.
- Mises à jour = Renovate → PR (jamais Watchtower).
- Monitoring = Beszel read-only sur tailnet, pas de WebUI maison.
- Secrets = sops-nix (phase 4).

---

## 13. Questions ouvertes

1. Confirmer la correction du `.gitignore` qui s'auto-ignore.
2. `AGENTS.md` et `CLAUDE.md` quasi identiques : factoriser (un fichier + un
   symlink/inclusion) ou accepter la duplication ?
3. Clé de déchiffrement sops : clé hôte SSH→age, ou clé age dédiée ?
4. Auto-merge Renovate pour les patchs `nixpkgs` mineurs : oui/non ?
5. Exposition des apps : tailnet only par défaut — exception un jour pour un
   service public (reverse proxy + ACME) ?
```
