# Déploiement stagé — bascule vers l'UI standalone

NixOS active une génération de façon **atomique** : la nouvelle génération se
construit entièrement puis bascule d'un coup, ou échoue sans rien changer. Il
n'y a donc pas d'état « moitié-appliqué ». Le risque résiduel n'est pas un
demi-déploiement mais : *la nouvelle génération démarre, mais oauth2-proxy
échoue au runtime (secret/cert manquant) → plus d'UE web, et Grafana est parti*.

Le staging réduit ce risque en deux temps, via le flag
`platform.observability.enabled`.

## Prérequis (à faire AVANT tout merge)

1. **App GitHub OAuth** : créer l'app, callback
   `https://<magicdns-name>/oauth2/callback`. Restreindre à l'org via
   `OAUTH2_GITHUB_ORG`.
2. **Secret SOPS `oauth2_proxy_env`** dans `secrets/homelab.yaml` :
   ```
   OAUTH2_PROXY_CLIENT_ID=...
   OAUTH2_PROXY_CLIENT_SECRET=...
   OAUTH2_PROXY_COOKIE_SECRET=<32-char string>
   ```
   Le cookie secret doit faire **16/24/32 caractères** (oauth2-proxy lit la
   string comme la clé AES). Générer 32 chars : `openssl rand -base64 24`
   (PAS `-base64 32`, qui sort 44 chars et fait crasher oauth2-proxy).
3. **npmDepsHash réel** dans `modules/control-api.nix` (`webPkg`) : remplacer
   `lib.fakeHash` par le hash que `nix build` imprime au premier build. Tant
   qu'il vaut `fakeHash`, le build **échoue** → la bascule n'a pas lieu (filet
   de sécurité, mais à corriger pour déployer).
4. **Rôles** : mapper ton compte dans `config/access.json` `users` (sinon tu es
   `viewer` read-only — c'est voulu : moindre privilège).
5. **Tailscale** : MagicDNS + HTTPS activés sur le tailnet.

## Étape 1 — Déployer en parallèle (observability ON)

État par défaut : `platform.observability.enabled = true`. Prometheus + Loki +
Tempo + Pyroscope continuent de tourner pendant que la nouvelle UI standalone
arrive via oauth2-proxy + tailscale serve.

1. Garder une **session SSH ouverte** sur l'hôte pendant le deploy.
2. Merger la branche → `deploy.yml` build + switch (atomique).
3. Vérifier :
   - `systemctl status oauth2-proxy control-api tailscale-serve` = active.
   - Ouvrir `https://<magicdns-name>/` → login GitHub → UI standalone répond,
     `/v1/me` renvoie ton email + rôle.
   - Les apps tournent, Prometheus a toujours l'historique.
4. Si l'UI est cassée : `nixos-rebuild --rollback switch` (ou rollback de
   génération) restaure l'état précédent ; corriger puis recommencer.

## Étape 2 — Teardown réversible (observability OFF)

Une fois l'UI standalone validée :

1. PR : `config/platform.nix` → `observability.enabled = false;`.
2. Merge → deploy. Prometheus/Loki/Tempo/Pyroscope sont retirés ; seule l'UI
   standalone reste.
3. Réversible : si besoin, repasser `enabled = true` (PR) restaure les data
   backends. L'historique métriques/logs avant teardown est perdu (volumes non
   sauvegardés).

## Rappel sécurité

- control-api écoute **loopback uniquement** ; il n'est joignable que via
  oauth2-proxy. Les rôles dérivent de l'identité oauth2-proxy, jamais d'un
  header client.
- `default_role = viewer` : tout compte authentifié est read-only tant qu'un
  rôle supérieur n'est pas accordé dans `access.json`.
