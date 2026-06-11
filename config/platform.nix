# Global platform configuration (HomeLab Platform V2).
#
# Plain attribute set, consumed by modules/platform.nix (validation +
# /etc/homelab/platform.json), by lib/storage.nix (path resolution) and by
# modules/apps.nix (defaults). This is the durable, declarative source of
# truth for host-wide behavior. It never contains secrets or data.
{
  host = {
    hostname = "homelab";
    timezone = "Europe/Paris";
    locale = "en_US.UTF-8";
  };

  network = {
    trustedInterfaces = [ "tailscale0" ];
    tailnet = "tailnet";
  };

  # Logical storage classes. Apps reference a class by name; lib/storage.nix
  # resolves <class>.basePath + app + volume into a concrete path.
  # backedUp = false marks data the backup module must skip (e.g. cache).
  # ephemeral = true marks volatile storage (tmpfs) that does not survive a
  # reboot — the policy engine refuses durable data (databases) on it.
  storageClasses = {
    local = { type = "local"; basePath = "/var/lib/homelab/data"; backedUp = true; };
    nas = { type = "nfs"; basePath = "/mnt/homelab"; backedUp = true; };
    fast = { type = "ssd"; basePath = "/var/lib/homelab/fast"; backedUp = true; };
    bulk = { type = "local"; basePath = "/mnt/bulk/homelab"; backedUp = true; };
    cache = { type = "local"; basePath = "/var/cache/homelab"; backedUp = false; };
    ephemeral = { type = "tmpfs"; basePath = "/run/homelab/ephemeral"; backedUp = false; ephemeral = true; };
  };

  defaultStorageClass = "local";

  backup = {
    backend = "restic";
    repository = ""; # set via secrets / env in production
    schedule = "daily";
    retention = { daily = 7; weekly = 4; monthly = 6; };
  };

  # Default update intent for apps that do not declare one.
  updatePolicyDefault = "manual";

  paths = {
    dataRoot = "/var/lib/homelab/data";
    secretsRoot = "/run/secrets";
  };

  # Apps are private (tailnet only) unless they explicitly request public-port.
  defaultVisibility = "private";

  # Observability is OPT-IN. v0.1 shipped none on purpose ("declare it as an
  # app"); v0.2 makes it a first-class module that stays OFF by default. When
  # enabled, modules/observability.nix runs a node_exporter on every host and a
  # Prometheus that scrapes it + control-api's /metrics. Everything binds to
  # loopback (reached over the tailnet via the existing front); Grafana, if you
  # want dashboards, is still declared as an ordinary app.
  observability = {
    enable = false;
    nodeExporter = {
      enable = true;
      port = 9100;
      # Bind loopback by default. To let a fleet Prometheus scrape this host,
      # set this to the host's tailscale address (and add it to extraTargets on
      # the scraping host).
      listenAddress = "127.0.0.1";
    };
    prometheus = {
      enable = true;
      port = 9090;
      retention = "15d";
      scrapeInterval = "15s";
      # Other hosts' node_exporters as "host:port"; requires those exporters to
      # listen on a reachable (tailnet) address.
      extraTargets = [ ];
    };
  };
}
