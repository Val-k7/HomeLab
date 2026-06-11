# Example schemaVersion=2 app. Demonstrates the V2 model end-to-end: typed
# runtime block, explicit permissions, criticality, update policy and a
# mandatory healthcheck. Kept low-criticality with a pinned tag so it passes
# policy validation in warn mode with no findings.
{
  schemaVersion = 2;
  source = "local";
  criticality = "low";
  updatePolicy = "manual";

  runtime = {
    runner = "image";
    image = "traefik/whoami";
    tag = "v1.11.0";
    port = 8090;
  };

  permissions = [ "tailnet-port" ];

  healthcheck = {
    type = "http";
    path = "/";
    timeoutSec = 5;
  };

  # Inter-app ordering (since v0.2). Each name must be another built app
  # (apps/<name>.nix); modules/apps.nix turns these into systemd after+wants on
  # app-<dep>.service, so this app starts after its dependencies. The eval-time
  # graph check rejects unknown names and self-dependencies.
  # dependencies = [ "postgres" "redis" ];
  dependencies = [ ];
}
