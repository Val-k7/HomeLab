# Eval-time tests for the multi-host platform merge (lib/load-platform.nix) and
# the opt-in observability defaults.
{ lib }:

let
  loadPlatform = hostName: import ../lib/load-platform.nix { inherit lib hostName; };

  # No overlay exists for these synthetic names, so the result is the shared
  # base with host.hostname injected from the flake host name.
  homelab = loadPlatform "homelab";
  edge = loadPlatform "edge";
  base = import ../config/platform.nix;

  checks = [
    {
      name = "hostname-tracks-flake-host";
      ok = homelab.host.hostname == "homelab" && edge.host.hostname == "edge";
    }
    {
      name = "base-fields-preserved";
      ok = edge.defaultStorageClass == base.defaultStorageClass
        && edge.storageClasses ? local;
    }
    {
      name = "observability-default-off";
      ok = (base.observability.enable or false) == false;
    }
    {
      name = "observability-shape";
      ok = (base.observability.prometheus.port or 0) == 9090
        && (base.observability.nodeExporter.port or 0) == 9100;
    }
    {
      # recursiveUpdate is deep: injecting host.hostname must not drop the
      # sibling host.timezone/locale from the base.
      name = "deep-merge-keeps-siblings";
      ok = edge.host ? timezone && edge.host ? locale;
    }
  ];

  failed = builtins.filter (c: !c.ok) checks;
in
if failed == [ ] then "ok"
else throw "multi-host test failures: ${builtins.toJSON (map (c: c.name) failed)}"
