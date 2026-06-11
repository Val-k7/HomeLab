# Update Conceptuelle

Ce document formalise les concepts retenus pour faire evoluer le homelab NixOS
existant vers une base plus propre, plus tracable et plus ergonomique.

L'objectif n'est pas de repartir de zero. Le depot contient deja une base
fonctionnelle: flake NixOS, modules, Tailscale, SSH durci, SOPS, monitoring,
Grafana, dashboards, `control-api`, scripts d'exploitation, Renovate et CI/CD
GitHub Actions.

La suite doit donc consister a clarifier les responsabilites, renforcer le
modele GitOps et rendre Grafana plus utile sans lui donner le pouvoir de
contourner Git.

## Vision

Le homelab doit rester:

- minimal;
- securise;
- reproductible;
- observable;
- pilotable;
- maintenable;
- evolutif par Pull Request.

La source de verite durable reste le depot Git.

Grafana devient le cockpit: il permet de voir l'etat, de demander un changement,
de suivre les Pull Requests, les checks, les deployments et l'audit.

`control-api` devient un backend interne limite, expose uniquement via le
tailnet, capable de transformer des intentions utilisateur en actions controlees.

## Regle D'Or

Toute mutation durable doit produire une Pull Request.

Toute action operationnelle directe doit etre temporaire, auditee, limitee et
reversible.

En d'autres termes:

```text
Grafana peut initier.
control-api peut generer.
GitHub doit valider.
CI doit verifier.
main seul peut deployer durablement.
```

## Deux Entrees, Un Seul Chemin De Verite

Deux chemins doivent coexister.

### GitOps Expert

Un utilisateur avance modifie directement le depot:

```text
branche -> commit -> Pull Request -> CI -> review -> merge -> deploy
```

Ce chemin reste le plus flexible.

### Grafana Formulaire Simple

Un utilisateur passe par Grafana pour des cas standards:

```text
formulaire Grafana -> control-api -> branche -> commit -> Pull Request -> CI -> review -> merge -> deploy
```

Grafana ne remplace pas GitOps. Grafana produit du GitOps assiste.

## Separation Des Plans

Le systeme doit etre pense en trois plans distincts.

### Observability Plane

Ce plan observe sans modifier.

Il couvre:

- etat des hosts;
- etat des apps;
- metriques;
- logs;
- traces;
- profils;
- alertes;
- historique des deployments;
- audit;
- etat des Pull Requests;
- etat des checks CI.

Grafana doit etre le plus natif possible sur cette partie.

### Change Plane

Ce plan produit des changements durables sous forme de Pull Requests.

Exemples:

- ajouter une app;
- mettre a jour une app;
- rollback versionne d'une app;
- update de `flake.lock`;
- changement de dashboard;
- changement d'alerte;
- activation future d'un module autorise.

Le `control-api` agit ici comme Change Gateway.

Il ne doit pas pousser sur `main`, merger, bypasser les protections de branche ou
appliquer directement une mutation durable.

### Ops Plane

Ce plan gere les actions temporaires d'exploitation.

Exemples:

- restart d'une app;
- dry-run;
- build;
- relance d'un deployment de `main`;
- rollback systeme d'urgence;
- reboot d'urgence.

Ces actions peuvent rester directes, mais doivent etre fortement encadrees:

- role suffisant;
- raison obligatoire pour les actions sensibles;
- double confirmation si risque eleve;
- audit;
- visibilite dans Grafana.

## Role De `control-api`

Le backend interne doit evoluer vers deux responsabilites explicites.

### Change Gateway

Pour les changements durables, il doit:

- valider une demande;
- generer les fichiers autorises;
- creer une branche;
- creer un commit;
- ouvrir une Pull Request;
- associer l'acteur, le type de changement, les fichiers touches et les checks attendus;
- exposer l'etat de la PR a Grafana.

Il ne doit pas:

- pousser sur `main`;
- merger;
- deployer une branche non mergee;
- modifier des fichiers non autorises;
- accepter du Nix arbitraire depuis un formulaire;
- stocker des secrets en clair.

### Ops Gateway

Pour les actions temporaires, il doit:

- executer uniquement les actions allowlistees;
- verifier les roles;
- demander confirmation si necessaire;
- enregistrer un evenement d'audit;
- retourner un `job_id` ou `audit_id`;
- exposer le resultat a Grafana.

## Interface Grafana Cible

L'interface Grafana doit etre complete, ergonomique et orientee workflow.

Elle doit clairement distinguer:

- observer;
- agir temporairement;
- proposer un changement durable.

### 1. Home

Vue de synthese.

Elle affiche:

- statut global: healthy, degraded, deploying, attention;
- generation NixOS active;
- commit deploye;
- dernier deployment;
- PR ouvertes;
- apps obsoletes;
- alertes actives;
- CPU, RAM, disque;
- raccourcis vers Apps, Changes, Deployments.

La home ne doit pas contenir d'action dangereuse.

### 2. Apps

Vue d'exploitation quotidienne.

Elle affiche:

- nom;
- runner;
- etat runtime;
- version actuelle;
- version upstream;
- port;
- metriques;
- dernier deployment;
- actions.

Actions par app:

- ouvrir l'app;
- voir les logs;
- restart;
- proposer update;
- proposer rollback;
- voir details.

Les actions temporaires et les actions PR doivent etre visuellement separees.

### 3. Ajouter Une App

Assistant en quatre etapes.

Etape 1: type de projet.

- Docker image;
- Docker Compose;
- Dockerfile depuis repo;
- process sans Docker.

Etape 2: source.

- image et tag/digest;
- repo et rev;
- compose YAML;
- runtime;
- build command;
- start command.

Etape 3: runtime.

- port interne;
- port expose tailnet;
- healthcheck;
- metriques;
- variables non sensibles;
- references vers secrets/envFile existants;
- volumes encadres.

Etape 4: preview PR.

Avant creation, l'utilisateur voit:

- resume humain;
- fichiers generes;
- diff prevu;
- nom de branche;
- titre de PR;
- checks attendus;
- risques ou warnings.

Le bouton final doit etre `Create Pull Request`, jamais `Deploy now`.

### 4. Changes / Pull Requests

Vue GitOps centrale.

Elle affiche:

- demandes ouvertes;
- type de changement;
- acteur;
- branche;
- PR;
- checks;
- review;
- mergeability;
- deployment associe apres merge.

Actions:

- ouvrir la PR;
- voir le resume;
- voir le diff;
- relancer les checks;
- fermer une demande creee par Grafana;
- suivre le deployment.

Le merge peut rester cote GitHub.

### 5. Deployments

Vue de suivi et d'exploitation du deploiement.

Elle affiche:

- job en cours;
- dernier deployment reussi;
- dernier echec;
- commit;
- PR source si connue;
- generation;
- duree;
- logs;
- healthcheck;
- rollback disponible.

Actions:

- dry-run de `main`;
- build de `main`;
- deploy de `main`;
- rollback systeme d'urgence.

`Deploy main` applique uniquement une verite deja mergee.

`Rollback system` est une action d'urgence.

### 6. System / Security

Vue de posture minimale.

Elle affiche:

- Tailscale actif;
- SSH limite au tailnet;
- password auth desactivee;
- Grafana OAuth actif;
- SOPS present;
- token Git present sans afficher sa valeur;
- firewall actif;
- services critiques;
- age de `flake.lock`;
- espace `/nix/store`;
- version nixpkgs.

Les changements reseau/securite restent hors formulaire simple au depart.

### 7. Audit

Vue de tracabilite.

Elle affiche:

- time;
- actor;
- source;
- action;
- target;
- risk;
- result;
- PR;
- commit;
- job.

Filtres:

- acteur;
- type;
- risque;
- resultat;
- periode;
- app;
- PR;
- job.

## UI Native Grafana Et Iframes

La cible est de maximiser l'UI native Grafana.

Regle proposee:

- metriques, logs, statuts, historiques: panels Grafana natifs;
- formulaires simples: actions/panels Grafana ou plugin si possible;
- assistant complexe: iframe temporaire acceptable;
- iframes reservees aux cas ou Grafana natif ne suffit pas encore.

Les iframes existantes ne sont pas a supprimer immediatement. Elles doivent
devenir progressivement l'exception.

## Catalogue Des Changements Autorises

Le catalogue est le contrat central entre Grafana, `control-api`, GitHub et Nix.

Grafana ne demande jamais une modification libre. Il demande un type de
changement connu, avec des champs valides.

Chaque type doit definir:

- objectif;
- roles autorises;
- champs du formulaire;
- fichiers modifiables;
- validations;
- niveau de risque;
- PR generee;
- checks CI attendus;
- comportement apres merge.

### Catalogue MVP

| Changement | Source | Durable | PR obligatoire | Risque |
| --- | --- | --- | --- | --- |
| Ajouter une app | Grafana / Git | Oui | Oui | Moyen |
| Mettre a jour une app | Grafana / Git / Renovate | Oui | Oui | Moyen |
| Rollback version app | Grafana / Git | Oui | Oui | Moyen / Eleve |
| Dry-run / build | Grafana / GitHub | Non | Non | Faible |
| Deploy `main` | Grafana / GitHub | Applique Git | Non si deja merge | Eleve |
| Rollback systeme | Grafana / GitHub | Urgence | Non | Critique |
| Update `flake.lock` | Grafana / Renovate / Git | Oui | Oui | Moyen |

### Changements V1

- modifier dashboard;
- modifier alerte;
- vue Changes/PR complete;
- roles GitHub;
- meilleur lien PR -> commit -> workflow -> deployment -> generation.

### Changements Plus Tard

- activer/desactiver module via formulaire;
- reseau/securite via formulaires stricts;
- secrets assistes;
- multi-host;
- plugin Grafana dedie si necessaire.

## Ajouter Une App

Premier cas a rendre excellent.

Champs possibles:

- nom;
- type: `docker-image`, `compose`, `dockerfile`, `process`;
- source: image ou repo;
- version, tag ou rev;
- port;
- metriques;
- healthcheck;
- variables non sensibles;
- references de secrets existants;
- exposition tailnet par defaut.

Fichiers autorises:

- `apps/<name>.nix`;
- eventuellement `apps/<name>/docker-compose.yml`.

Interdits:

- modification de `modules/*`;
- modification de `.github/*`;
- secrets en clair;
- port hors tailnet par defaut;
- Nix arbitraire.

PR generee:

```text
apps: add <name>
```

Checks:

- nom valide;
- port non conflictuel;
- module Nix valide;
- pas de secret en clair;
- evaluation Nix;
- build ou dry-run selon contexte.

## Mettre A Jour Une App

But: remplacer `rev`, tag ou digest.

Sources:

- bouton Grafana `Propose update`;
- Renovate;
- PR manuelle.

Fichiers autorises:

- `apps/<name>.nix`;
- eventuellement lock/checksum lie a l'app.

PR generee:

```text
apps: update <name> <old> -> <new>
```

Validation:

- app existante;
- nouvelle version resoluble;
- diff limite a l'app;
- pas de changement structurel inattendu.

## Rollback Version App

Rollback durable d'une app.

Ce n'est pas un rollback runtime. C'est une PR qui remet une ancienne version.

Champs:

- app;
- version actuelle;
- version cible;
- raison;
- deployment historique source si disponible.

Fichiers autorises:

- `apps/<name>.nix`.

PR generee:

```text
apps: rollback <name> <current> -> <target>
```

Validation:

- raison obligatoire;
- cible presente dans l'historique ou explicitement autorisee;
- diff limite a l'app.

## Dry-Run, Build Et Deploy

`dry-run` et `build` ne modifient pas la verite Git.

Ils peuvent etre lances depuis Grafana et doivent etre audites.

`Deploy main` est une action operationnelle qui applique une verite deja mergee.

Validation:

- ref autorisee;
- checks verts;
- acteur autorise;
- confirmation forte si necessaire.

Il ne faut pas deployer une branche arbitraire comme si elle etait validee.

## Rollback Systeme

Le rollback systeme est une action d'urgence.

Il peut rester direct, mais il doit etre:

- reserve admin;
- double confirme;
- motive par une raison obligatoire;
- audite;
- visible dans Grafana;
- eventuellement suivi par une issue ou PR post-incident.

## Secrets

Le formulaire Grafana ne doit jamais stocker de secret en clair dans Git.

Deux niveaux:

- referencer un secret existant: possible;
- creer ou modifier un secret: manuel/SOPS au depart.

Au debut, Grafana doit seulement permettre de referencer des secrets ou envFiles
existants.

## Autorisation

Les roles doivent etre verifies par Grafana et par `control-api`.

Roles conceptuels:

- viewer: voir dashboards, logs, PR, deployments;
- operator: restart app, dry-run, creer PR simple;
- maintainer: PR sensibles, rollback app, relancer deploy;
- admin: rollback systeme, reboot, securite.

Preference:

- identite depuis GitHub OAuth;
- equipes GitHub comme source principale;
- mapping de roles versionne dans le depot.

## Branches Et Pull Requests Generees

Convention de branches proposee:

```text
change/app-add/<name>-<timestamp>
change/app-update/<name>-<timestamp>
change/app-rollback/<name>-<timestamp>
change/flake-update/<timestamp>
change/module/<name>-<timestamp>
```

Titres de PR:

```text
apps: add whoami
apps: update whoami abc123 -> def456
apps: rollback whoami def456 -> abc123
system: update flake.lock
modules: enable backups
```

## Contrat API Conceptuel Du Change Gateway

L'API doit etre organisee en quatre familles.

### Lecture

Endpoints conceptuels:

```text
GET /v1/catalog
GET /v1/apps
GET /v1/changes
GET /v1/deployments
GET /v1/audit
```

`GET /v1/catalog` expose les changements autorises.

`GET /v1/apps` expose les apps declarees, leur etat, leurs versions et actions.

`GET /v1/changes` expose les PR et demandes connues.

`GET /v1/deployments` expose l'historique et les jobs.

### Ops Temporaires

Endpoints conceptuels:

```text
POST /v1/ops/app/restart
POST /v1/ops/deploy-main
POST /v1/ops/system-rollback
```

Ces endpoints retournent un `job_id` ou `audit_id`, pas une PR.

### Changements PR-First

Endpoints conceptuels:

```text
POST /v1/changes/app-add/preview
POST /v1/changes/app-add
POST /v1/changes/app-update
POST /v1/changes/app-rollback
POST /v1/changes/flake-update
```

Ces endpoints retournent un `change_id`, une branche et une PR.

Ils ne deployent pas directement.

### GitHub / CI

Endpoints conceptuels:

```text
GET /v1/github/prs
POST /v1/github/checks/rerun
POST /v1/github/workflows/dry-run
```

Ces endpoints permettent a Grafana de suivre ou relancer les checks sans
modifier la verite Git.

## Reponses Attendues

Toute mutation durable doit repondre avec:

```json
{
  "change_id": "chg_123",
  "branch": "change/app-add/whoami-20260607",
  "pr": {
    "number": 42,
    "url": "https://github.com/org/repo/pull/42"
  }
}
```

Toute action ops doit repondre avec:

```json
{
  "job_id": "ops_123",
  "audit_id": "aud_123"
}
```

## CI Gates

Les checks doivent dependre du type de changement.

Base minimale:

- evaluation Nix;
- `nix flake check`;
- build host;
- tests Go;
- shellcheck;
- validation dashboards;
- detection de secrets en clair.

Pour les apps:

- schema app valide;
- port non conflictuel;
- runner valide;
- diff limite;
- exposition tailnet par defaut.

## Observabilite Des Changements

Grafana doit pouvoir relier:

```text
PR -> commit -> workflow -> deployment -> generation -> etat runtime
```

Chaque deployment devrait connaitre:

- PR source;
- commit;
- acteur;
- type de changement;
- generation NixOS;
- resultat;
- rollback disponible.

## Priorite MVP

Ordre recommande:

1. Transformer les changements d'apps en PR-first: add, update, rollback.
2. Ajouter la vue `Changes / Pull Requests`.
3. Garder les ops directes existantes, mais mieux les nommer et les separer.
4. Ameliorer le formulaire `Create app PR` dans `Apps` avec preview PR.
5. Ajouter une vraie notion d'acteur et de role cote backend.
6. Reduire les iframes apres stabilisation fonctionnelle.

La priorite n'est pas d'ajouter plus d'applications, mais de rendre le socle
coherent, tracable et pret a evoluer.
