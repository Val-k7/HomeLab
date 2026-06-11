# Opt-in observability (HomeLab Platform V2, since v0.2).
#
# OFF unless config/platform.nix (or a host overlay) sets
# observability.enable = true. When on:
#   * a node_exporter publishes host metrics, and
#   * a Prometheus scrapes the node_exporter and control-api's /metrics,
# both bound to loopback by default (reached over the tailnet via the existing
# oauth2-proxy / tailscale serve front, like control-api). This module never
# opens a firewall port. Dashboards (Grafana) remain an ordinary declared app.
{ config, lib, pkgs, env, hostName ? "homelab", ... }:

let
  e = import ../lib/env-lib.nix { inherit lib env; };
  controlApiPort = e.getInt "CONTROL_API_PORT" 9092;

  platform = import ../lib/load-platform.nix { inherit lib hostName; };
  obs = platform.observability or { };
  enabled = obs.enable or false;

  ne = obs.nodeExporter or { };
  neEnable = ne.enable or true;
  nePort = ne.port or 9100;
  neAddr = ne.listenAddress or "127.0.0.1";

  neIsLoopback = neAddr == "127.0.0.1" || neAddr == "localhost" || neAddr == "::1";

  prom = obs.prometheus or { };
  promEnable = prom.enable or true;
  promPort = prom.port or 9090;
  retention = prom.retention or "15d";
  scrapeInterval = prom.scrapeInterval or "15s";
  extraTargets = prom.extraTargets or [ ];
in
lib.mkIf enabled {
  assertions = [
    {
      assertion = !promEnable || neEnable || extraTargets != [ ];
      message = "observability.prometheus is enabled but there is nothing to scrape (node_exporter disabled and no extraTargets).";
    }
  ];

  # exporters.node and the server both live under services.prometheus, so they
  # must be merged into one definition (two `services.prometheus.*` keys in the
  # same attrset would collide). Each half stays independently gated.
  services.prometheus = lib.mkMerge [
    (lib.mkIf neEnable {
      exporters.node = {
        enable = true;
        port = nePort;
        listenAddress = neAddr;
        enabledCollectors = [ "systemd" ];
      };
    })
    (lib.mkIf promEnable {
      enable = true;
      port = promPort;
      listenAddress = "127.0.0.1";
      retentionTime = retention;
      globalConfig.scrape_interval = scrapeInterval;
      scrapeConfigs = [
        {
          job_name = "node";
          static_configs = [{
            targets = (lib.optional neEnable "127.0.0.1:${toString nePort}") ++ extraTargets;
            labels.host = hostName;
          }];
        }
        {
          job_name = "control-api";
          metrics_path = "/metrics";
          static_configs = [{
            targets = [ "127.0.0.1:${toString controlApiPort}" ];
            labels.host = hostName;
          }];
        }
      ];
    })
  ];

  # node_exporter binds loopback by default (a same-host Prometheus reaches it
  # with no firewall change). When it is bound to a non-loopback address for a
  # fleet Prometheus to scrape, open that port on the tailnet interface only —
  # otherwise the default-on firewall silently drops the scrape.
  networking.firewall.interfaces.tailscale0.allowedTCPPorts =
    lib.optional (neEnable && !neIsLoopback) nePort;
}
