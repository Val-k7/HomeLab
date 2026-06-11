# Eval-time tests for the app model normalizer.
# Returns "ok" or throws with the list of failed checks. Wired into flake
# checks via tests/default.nix so `nix flake check` runs it.
{ lib }:

let
  platform = import ../config/platform.nix;
  m = import ../lib/app-model.nix { inherit lib platform; };

  v1 = m.normalize { runner = "compose"; dir = ./.; port = 8088; };

  v2 = m.normalize {
    schemaVersion = 2;
    source = "workshop";
    runtime = {
      runner = "image";
      image = "traefik/whoami";
      tag = "v1.11.0";
      digest = "sha256:deadbeef";
      port = 8080;
    };
    criticality = "high";
    updatePolicy = "autoLow";
    permissions = [ "tailnet-port" ];
    volumes = [ { name = "config"; kind = "config"; class = "nas"; } ];
    secrets = [ { name = "api_key"; required = true; } ];
    metrics = { enabled = true; path = "/m"; };
  };

  checks = [
    { name = "v1-schemaVersion"; ok = v1.schemaVersion == 1; }
    { name = "v1-runner"; ok = v1.runner == "compose"; }
    { name = "v1-port"; ok = v1.port == 8088; }
    { name = "v1-default-criticality"; ok = v1.criticality == "low"; }
    { name = "v1-default-updatePolicy"; ok = v1.updatePolicy == platform.updatePolicyDefault; }
    { name = "v1-infers-tailnet-port"; ok = lib.elem "tailnet-port" v1.permissions; }
    { name = "v1-empty-volumes"; ok = v1.volumes == [ ]; }
    { name = "v2-schemaVersion"; ok = v2.schemaVersion == 2; }
    { name = "v2-flatten-runner"; ok = v2.runner == "image"; }
    { name = "v2-digest"; ok = v2.digest == "sha256:deadbeef"; }
    { name = "v2-criticality"; ok = v2.criticality == "high"; }
    { name = "v2-updatePolicy"; ok = v2.updatePolicy == "autoLow"; }
    { name = "v2-source"; ok = v2.source == "workshop"; }
    { name = "v2-volume-class"; ok = (builtins.head v2.volumes).class == "nas"; }
    { name = "v2-secret-name"; ok = (builtins.head v2.secrets).name == "api_key"; }
    { name = "v2-metrics-enabled"; ok = v2.metrics == true; }
    { name = "v2-metrics-path"; ok = v2.metricsPath == "/m"; }
  ];

  failed = builtins.filter (c: !c.ok) checks;
in
if failed == [ ] then "ok"
else throw "app-model test failures: ${builtins.toJSON (map (c: c.name) failed)}"
