# Platform configuration module (HomeLab Platform V2).
#
# Reads config/platform.nix, config/policies.nix, config/catalogs.nix,
# validates them with assertions, and publishes them read-only at
# /etc/homelab/{platform,policies,catalogs}.json for the control-api and the
# CI validator. It does not apply host identity (timezone/locale already come
# from .env in hosts/homelab/configuration.nix) so importing it changes no
# runtime behavior — only adds the manifests.
#
# Multi-host: config/platform.nix is the shared base. A host may override any
# field with hosts/<name>/platform.nix (deep-merged on top). The published
# host.hostname always tracks the flake host name unless the overlay sets it
# explicitly, so control-api on each host knows which host it is.
{ lib, hostName ? "homelab", ... }:

let
  platform = import ../lib/load-platform.nix { inherit lib hostName; };
  policies = import ../lib/load-policies.nix { inherit lib hostName; };
  catalogsFile = ../config/catalogs.nix;
  catalogs = if builtins.pathExists catalogsFile then import catalogsFile else { catalogs = [ ]; };

  validUpdatePolicies = [ "manual" "autoLow" "critical" ];
  validStorageTypes = [ "local" "nfs" "ssd" "tmpfs" ];
  validTrust = [ "official" "community" "untrusted" ];
  validCatalogPolicy = [ "strict" "warn" ];
  validCatalogCategory = [ "media" "network" "dev" "data" "monitoring" "misc" ];
  movingRefs = [ "latest" "stable" "main" "master" "release" "edge" "HEAD" ];

  catalogList = catalogs.catalogs or [ ];

  storageClassNames = lib.attrNames platform.storageClasses;
in
{
  assertions = [
    {
      assertion = lib.elem platform.updatePolicyDefault validUpdatePolicies;
      message = "platform.updatePolicyDefault must be one of ${lib.concatStringsSep ", " validUpdatePolicies}";
    }
    {
      assertion = lib.elem platform.defaultStorageClass storageClassNames;
      message = "platform.defaultStorageClass '${platform.defaultStorageClass}' is not a declared storage class";
    }
    {
      assertion = lib.all (n: lib.elem platform.storageClasses.${n}.type validStorageTypes) storageClassNames;
      message = "platform.storageClasses.<name>.type must be one of ${lib.concatStringsSep ", " validStorageTypes}";
    }
    {
      assertion = lib.all (c: lib.elem (c.trust or "untrusted") validTrust) catalogList;
      message = "config/catalogs.nix: catalog trust must be one of ${lib.concatStringsSep ", " validTrust}";
    }
    {
      assertion = lib.all (c: (c ? id) && (c ? repo) && (c ? ref)) catalogList;
      message = "config/catalogs.nix: each catalog needs id, repo and ref (ref must be a tag or SHA, not a moving branch)";
    }
    {
      # A catalog ref must be immutable: a moving branch silently changes what is
      # installed, defeating the pin. Reject the known moving aliases.
      assertion = lib.all (c: !(lib.elem (c.ref or "") movingRefs)) catalogList;
      message = "config/catalogs.nix: catalog ref must be a tag or commit SHA, not a moving branch (${lib.concatStringsSep ", " movingRefs})";
    }
    {
      assertion = lib.all (c: lib.elem (c.policy or "strict") validCatalogPolicy) catalogList;
      message = "config/catalogs.nix: catalog policy must be one of ${lib.concatStringsSep ", " validCatalogPolicy}";
    }
    {
      assertion = lib.all (c: !(c ? category) || lib.elem c.category validCatalogCategory) catalogList;
      message = "config/catalogs.nix: catalog category must be one of ${lib.concatStringsSep ", " validCatalogCategory}";
    }
    {
      # ids must be unique so /v1/library and the clone dir do not collide.
      assertion = (lib.length (lib.unique (map (c: c.id or "") catalogList))) == (lib.length catalogList);
      message = "config/catalogs.nix: catalog ids must be unique";
    }
  ];

  environment.etc."homelab/platform.json" = { text = builtins.toJSON platform; mode = "0444"; };
  environment.etc."homelab/policies.json" = { text = builtins.toJSON policies; mode = "0444"; };
  environment.etc."homelab/catalogs.json" = { text = builtins.toJSON catalogs; mode = "0444"; };
}
