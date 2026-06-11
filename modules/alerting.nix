# Minimal failure alerting (HomeLab Platform V2).
#
# An `alert@.service` template fires on unit failure (attached via onFailure
# here and in apps.nix/backup.nix) and POSTs hostname, unit, timestamp and the
# last journal lines to a webhook. The webhook URL is read AT RUNTIME from
# /run/secrets/alert_webhook (sops): when the secret is absent the script logs
# and exits 0, so the module carries zero configuration burden and no
# eval-time dependency on secret presence. ntfy-style URLs get a plain body
# with a Title header; anything else gets JSON {"text": ...} (Slack/Discord/
# generic). Delivery is best-effort: a failed POST never fails anything.
{ config, lib, pkgs, hostName ? "homelab", ... }:

let
  cfg = config.homelab.alerting;
  webhookFile = "/run/secrets/alert_webhook";

  platform = import ../lib/load-platform.nix { inherit lib hostName; };
  storagePaths = lib.unique (lib.mapAttrsToList (_: c: c.basePath)
    (lib.filterAttrs (_: c: !(c.ephemeral or false)) (platform.storageClasses or { })));

  # hl-alert <title> <body>: shared delivery helper (also handy interactively).
  hlAlert = pkgs.writeShellScriptBin "hl-alert" ''
    set -u
    title="''${1:-homelab alert}"
    body="''${2:-}"
    if [ ! -r ${webhookFile} ]; then
      echo "hl-alert: no webhook configured (${webhookFile} absent); skipping: $title"
      exit 0
    fi
    url="$(cat ${webhookFile})"
    if [ -z "$url" ]; then
      echo "hl-alert: ${webhookFile} is empty; skipping: $title"
      exit 0
    fi
    case "$url" in
      *ntfy*)
        ${pkgs.curl}/bin/curl -fsS --max-time 10 \
          -H "Title: $title" --data-binary "$body" "$url" \
          || echo "hl-alert: delivery failed (ntfy): $title"
        ;;
      *)
        ${pkgs.jq}/bin/jq -cn --arg t "$title" --arg b "$body" '{text: ($t + "\n" + $b)}' \
          | ${pkgs.curl}/bin/curl -fsS --max-time 10 \
              -H "Content-Type: application/json" --data-binary @- "$url" \
          || echo "hl-alert: delivery failed (json): $title"
        ;;
    esac
    exit 0
  '';

  # Body builder for alert@<unit>: hostname, unit, timestamp, last 20 journal
  # lines of the failed unit.
  alertUnitScript = pkgs.writeShellScript "hl-alert-unit" ''
    set -u
    unit="''${1:-unknown}"
    logs="$(${pkgs.systemd}/bin/journalctl -u "$unit" -n 20 --no-pager 2>&1 || true)"
    ${hlAlert}/bin/hl-alert "[${hostName}] $unit failed" "host: ${hostName}
unit: $unit
time: $(date -u +%Y-%m-%dT%H:%M:%SZ)

$logs" || true
    exit 0
  '';

  diskWatchScript = pkgs.writeShellScript "hl-disk-watch" ''
    set -u
    threshold=90
    report=""
    for p in / ${lib.concatStringsSep " " (map lib.escapeShellArg storagePaths)}; do
      [ -d "$p" ] || continue
      use="$(df --output=pcent "$p" 2>/dev/null | tail -1 | tr -dc '0-9')"
      [ -n "$use" ] || continue
      if [ "$use" -gt "$threshold" ]; then
        report="$report$p at ''${use}% (threshold ''${threshold}%)
"
      fi
    done
    if [ -n "$report" ]; then
      ${hlAlert}/bin/hl-alert "[${hostName}] disk space warning" "$report" || true
    fi
    exit 0
  '';

  onFail = { onFailure = [ "alert@%n.service" ]; };
in
{
  options.homelab.alerting.enable = lib.mkOption {
    type = lib.types.bool;
    default = true;
    description = "Enable webhook failure alerting (no-ops gracefully when /run/secrets/alert_webhook is absent).";
  };

  config = lib.mkIf cfg.enable {
    environment.systemPackages = [ hlAlert ];

    systemd.services = {
      # Template: alert@<failed-unit>.service (instantiated via onFailure=%n).
      "alert@" = {
        description = "failure alert for %i";
        path = [ pkgs.coreutils ];
        serviceConfig = {
          Type = "oneshot";
          ExecStart = "${alertUnitScript} %i";
        };
      };

      alert-disk-watch = {
        description = "disk usage check (alert above 90%)";
        path = [ pkgs.coreutils ];
        serviceConfig = {
          Type = "oneshot";
          ExecStart = "${diskWatchScript}";
        };
      };

      # Attach failure alerts to the core platform units. NixOS merges
      # systemd.services across modules, so these only ADD onFailure to units
      # defined elsewhere (control-api.nix, docker.nix, auth.nix, tailscale.nix).
      control-api = onFail;
      docker = onFail;
      oauth2-proxy = onFail;
      tailscaled = onFail;
      "hl-deploy@" = onFail;
      "hl-backup@" = onFail;
    };

    systemd.timers.alert-disk-watch = {
      description = "schedule daily disk usage check";
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnCalendar = "*-*-* 08:00:00";
        Persistent = true;
        RandomizedDelaySec = "15m";
      };
    };
  };
}
