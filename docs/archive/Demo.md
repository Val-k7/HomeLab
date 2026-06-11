# Demo - Parcours de test Homelab GitOps

Ce document sert de check-list de demo apres deploiement. Les actions PR-first ne
doivent pas modifier `main` directement: elles doivent creer une Pull Request.

## Pre-requis

- Ouvrir Grafana depuis le tailnet.
- Verifier que l'utilisateur a au moins le role `operator` pour les creations de
  PR et `admin` pour les actions de deploiement systeme.
- Verifier que le datasource Grafana `Homelab API` repond.
- Verifier que le token GitHub de `/run/secrets/git_token` permet de pousser une
  branche et de creer une Pull Request.

## Donnees de test rapides

App Docker image:

| Champ | Valeur |
| --- | --- |
| Mode | `Image` |
| Name | `whoami-demo` |
| Image | `traefik/whoami` |
| Target | `v1.11.0` |
| Port | `8099` |
| Reason | `Demo add image app from Grafana` |

App repo Dockerfile:

| Champ | Valeur |
| --- | --- |
| Mode | `Repo` |
| Name | `whoami-dockerfile-demo` |
| Repo | `https://github.com/traefik/whoami.git` |
| Rev | `main` |
| Target | `main` |
| Advanced / Runner | `Dockerfile` |
| Port | `8100` |
| Reason | `Demo add dockerfile repo from Grafana` |

App process:

| Champ | Valeur |
| --- | --- |
| Mode | `Repo` |
| Name | `process-demo` |
| Repo | `https://github.com/example/demo-node-app.git` |
| Rev | `main` |
| Target | `main` |
| Advanced / Runner | `Process` |
| Runtime | `nodejs_22` |
| Build cmd | `npm ci` |
| Start cmd | `npm start` |
| Port | `8101` |
| Reason | `Demo add process app from Grafana` |

App compose:

| Champ | Valeur |
| --- | --- |
| Mode | `Repo` |
| Name | `compose-demo` |
| Repo | `https://github.com/example/demo-compose-app.git` |
| Rev | `main` |
| Target | `main` |
| Advanced / Runner | `Compose` |
| Compose dir | `.` |
| Port | `8102` |
| Reason | `Demo add compose app from Grafana` |

Exemple `docker-compose.yml`:

```yaml
services:
  app:
    image: traefik/whoami:v1.11.0
    ports:
      - "8102:80"
```

## 1. Home

1. Ouvrir le dashboard `Home`.
2. Verifier les stats `Apps`, `Infra`, `Generation`, `Deploy`, `CPU %`,
   `RAM %`.
3. Verifier que les tables `Apps`, `Recent changes` et `Recent audit`
   affichent des lignes.
4. Cliquer sur `Kiosk`, verifier que l'URL ajoute `kiosk=1`.
5. Cliquer a nouveau sur `Kiosk`, verifier que `kiosk=1` disparait.

Resultat attendu: affichage natif Grafana, aucune iframe visible, aucune erreur
de panel.

## 2. Apps - Create app PR

1. Ouvrir `Apps`.
2. Dans `Create app PR`, remplir les donnees `App Docker image`.
3. Cliquer `Create PR`.
4. Verifier la notification Grafana.
5. Ouvrir `Changes`.
6. Verifier qu'une PR apparait avec un `branch`, un `commit`, un numero de PR et
   une URL.

Resultat attendu: creation d'une PR, aucun deploy direct, aucun push direct sur
`main`.

## 3. Apps - Preview

1. Ouvrir `Apps`.
2. Dans `Create app PR`, remplir les donnees `App Docker image`.
3. Tester la preview depuis l'API si le panel Grafana ne propose pas de bouton
   preview separe:

```bash
curl -fsS -X POST http://127.0.0.1:9092/v1/changes/app-add/preview \
  -H 'Content-Type: application/json' \
  -d '{"name":"whoami-demo","mode":"image","image":"traefik/whoami","tag":"v1.11.0","port":8099,"reason":"Demo add image app from Grafana"}'
```

Resultat attendu: preview JSON avec `summary`, `files`, `risk`, `checks` et
eventuels `warnings`.

## 4. Apps - Advanced

1. Ouvrir `Apps`.
2. Dans `Create app PR`, activer `Advanced`.
3. Tester successivement les donnees `App repo Dockerfile`, `App process` et
   `App compose`.
4. Verifier que `Runner`, `Runtime`, `Build cmd`, `Start cmd`, `Compose dir` et
   `Compose YAML` restent lisibles et dans le panel.

Resultat attendu: les options avancees restent internes au formulaire et ne
debordent pas.

## 5. Apps - Update PR

1. Ouvrir `Apps`.
2. Dans `Update PR`, remplir:
   - `App`: `whoami`
   - `Target version`: `v1.11.0`
   - `Reason`: `Demo update from Grafana`
3. Cliquer `Create PR`.
4. Ouvrir `Changes`.

Resultat attendu: une PR `apps: update whoami ... -> v1.11.0` est creee.

## 6. Apps - Rollback PR

1. Ouvrir `Apps`.
2. Dans `Rollback PR`, remplir:
   - `App`: `whoami`
   - `Target version`: `v1.10.0`
   - `Reason`: `Demo rollback after validation`
3. Cliquer `Create PR`.
4. Ouvrir `Changes`.

Resultat attendu: une PR de rollback est creee. Sans `Reason`, l'API doit
refuser la demande.

## 7. Apps - Restart

1. Ouvrir `Apps`.
2. Dans `Restart`, remplir:
   - `App`: `whoami`
3. Cliquer `Restart`.
4. Ouvrir `Audit`.

Resultat attendu: action directe auditee sur `app-whoami.service`.

## 8. Services - Restart infra

1. Ouvrir `Services`.
2. Verifier la table `Infra actions`.
3. Dans `Restart infra`, remplir:
   - `Service`: `docker`
4. Cliquer `Restart`.
5. Ouvrir `Audit`.

Resultat attendu: action auditee. A utiliser seulement si le redemarrage Docker
est acceptable pendant la demo.

## 9. Deployments - Dry run et build

1. Ouvrir `Deployments`.
2. Cliquer `Run` sur `Dry run`.
3. Verifier `Running jobs` ou `History`.
4. Cliquer `Build`.
5. Verifier `History`.

Resultat attendu: jobs de validation lances sans appliquer de changement
systeme.

## 10. Deployments - Deploy merged main

1. Ouvrir `Deployments`.
2. Dans `Deploy merged main`, laisser:
   - `Mode`: `switch`
   - `Confirm ID`: vide
3. Cliquer `Deploy`.
4. Copier le `Confirm ID` retourne par Grafana.
5. Renseigner ce `Confirm ID`.
6. Cliquer `Deploy` une seconde fois.

Resultat attendu: le deploy ne part qu'apres double confirmation et cree un job
audite.

## 11. Deployments - System rollback

1. Ouvrir `Deployments`.
2. Dans `System rollback`, remplir:
   - `Mode`: `rollback`
   - `Generation`: une generation connue, par exemple `102`
   - `Confirm ID`: vide
3. Cliquer `Rollback`.
4. Copier le `Confirm ID`.
5. Renseigner ce `Confirm ID`.
6. Cliquer `Rollback` une seconde fois.

Resultat attendu: rollback systeme lance seulement apres double confirmation.

## 12. System

1. Ouvrir `System`.
2. Verifier les stats `CPU %`, `RAM %`, `Disk %`, `Active units`,
   `Failed units`, `Containers`.
3. Verifier les tables `Services`, `Containers`, `System audit`.
4. Pour tester le reboot, utiliser `Reboot host` avec la meme logique de double
   confirmation que le deploy.

Resultat attendu: donnees systeme visibles et action reboot bloquee tant que le
`Confirm ID` n'est pas fourni.

## 13. Audit

1. Ouvrir `Audit`.
2. Verifier `Operational audit`.
3. Verifier `UI audit`.
4. Confirmer que les actions precedentes apparaissent avec `actor`, `op`,
   `kind`, `target`, `risk`, `result`, `status` et, si applicable, `error`.

Resultat attendu: chaque mutation ou tentative bloquee laisse une trace.

## 14. Tests API rapides

Depuis le tailnet ou depuis l'hote:

```bash
curl -fsS http://127.0.0.1:9092/v1/catalog
curl -fsS http://127.0.0.1:9092/v1/apps
curl -fsS http://127.0.0.1:9092/v1/changes
curl -fsS http://127.0.0.1:9092/v1/deployments
curl -fsS http://127.0.0.1:9092/v1/audit?limit=20
```

Test legacy attendu:

```bash
curl -fsS -X POST http://127.0.0.1:9092/v1/apply \
  -H 'Content-Type: application/json' \
  -d '{"app":"whoami"}'
```

Resultat attendu: reponse `410 Gone` avec un message indiquant
`/v1/changes/app-update`.
