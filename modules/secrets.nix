{ lib, ... }:

let
  sopsFile = ../secrets/homelab.yaml;
  appSecretsDir = ../secrets/apps;

  # Top-level keys of a SOPS yaml file. SOPS encrypts values but leaves the
  # mapping KEYS in cleartext, so we can enumerate the declared secrets by
  # scanning lines like `KEY: ENC[...]` (excluding the trailing `sops:` block).
  keysOf = file:
    let
      lines = lib.splitString "\n" (builtins.readFile file);
      match = l: builtins.match "([A-Za-z_][A-Za-z0-9_]*):.*" l;
      keys = lib.filter (k: k != null && k != "sops") (map (l: let m = match l; in if m == null then null else builtins.head m) lines);
    in lib.unique keys;

  appSecretFiles =
    if builtins.pathExists appSecretsDir then
      lib.filterAttrs (n: t: t == "regular" && lib.hasSuffix ".yaml" n)
        (builtins.readDir appSecretsDir)
    else { };

  # Build sops.secrets entries for every key in every secrets/apps/<app>.yaml.
  appSecrets = lib.listToAttrs (lib.flatten (lib.mapAttrsToList
    (file: _:
      let f = appSecretsDir + "/${file}";
      in map
        (key: lib.nameValuePair key {
          sopsFile = f;
          inherit key;
          mode = "0440";
          group = "controlapi";
        })
        (keysOf f))
    appSecretFiles));

  # Declared only when actually present in secrets/homelab.yaml so a deploy
  # before provisioning the oauth secret does not fail activation.
  systemSecretKeys = if builtins.pathExists sopsFile then keysOf sopsFile else [ ];

  # System secrets provisioned from the control plane: one sops file per key
  # under secrets/system/<key>.yaml (written by the /v1/changes/system-secret
  # PR flow). Each known key gets its canonical owner/mode; these declarations
  # OVERRIDE the legacy homelab.yaml ones, so a UI rotation wins.
  systemSecretsDir = ../secrets/system;
  systemSecretModes = {
    restic_password = { mode = "0400"; };
    alert_webhook = { mode = "0400"; };
    tailscale_authkey = { mode = "0400"; };
    oauth2_proxy_env = { mode = "0400"; };
  };
  systemSecretFiles =
    if builtins.pathExists systemSecretsDir then
      lib.filterAttrs (n: t: t == "regular" && lib.hasSuffix ".yaml" n)
        (builtins.readDir systemSecretsDir)
    else { };
  uiSystemSecrets = lib.listToAttrs (lib.filter (e: e != null) (lib.mapAttrsToList
    (file: _:
      let key = lib.removeSuffix ".yaml" file;
      in if systemSecretModes ? ${key} then
        lib.nameValuePair key ({ sopsFile = systemSecretsDir + "/${file}"; inherit key; } // systemSecretModes.${key})
      else null)
    systemSecretFiles));
in
lib.mkIf (builtins.pathExists sopsFile) {
  sops.defaultSopsFile = sopsFile;
  systemd.tmpfiles.rules = [ "z /var/lib/sops/age/keys.txt 0400 root root - -" ];
  sops.age.keyFile = "/var/lib/sops/age/keys.txt";
  sops.age.generateKey = false;

  # NOTE: git_token is intentionally NOT a sops secret anymore. The deploy
  # workflow writes it directly to /var/lib/homelab-secrets/git_token on the
  # host (see .github/workflows/deploy.yml + modules/control-api.nix), so
  # rotation no longer requires re-encrypting and committing a sops value.
  sops.secrets = {
  }
  # Env file for oauth2-proxy (CLIENT_ID/SECRET + COOKIE_SECRET). Read by
  # systemd (root) as the unit's EnvironmentFile, so root:0400 is correct.
  // lib.optionalAttrs (lib.elem "oauth2_proxy_env" systemSecretKeys) {
    "oauth2_proxy_env" = { mode = "0400"; };
  }
  # Tailscale auth key. Read by root during tailscale activation, so root:0400.
  // lib.optionalAttrs (lib.elem "tailscale_authkey" systemSecretKeys) {
    "tailscale_authkey" = { mode = "0400"; };
  }
  # restic repository password for the backup module (v0.4). Read by the
  # oneshot backup/restore-test units (run as root), so root:0400. Declared only
  # when provisioned so a deploy before backups are configured does not fail.
  // lib.optionalAttrs (lib.elem "restic_password" systemSecretKeys) {
    "restic_password" = { mode = "0400"; };
  }
  # Alerting webhook URL (ntfy topic or Slack/Discord/generic endpoint). Read
  # at runtime by the alert@/disk-watch oneshot units (run as root), so
  # root:0400. Declared only when provisioned; the alerting module no-ops
  # gracefully when the file is absent.
  // lib.optionalAttrs (lib.elem "alert_webhook" systemSecretKeys) {
    "alert_webhook" = { mode = "0400"; };
  }
  // appSecrets
  // uiSystemSecrets;
}
