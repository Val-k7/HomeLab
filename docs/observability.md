# Observability (opt-in)

How metrics and monitoring work on the platform: an off-by-default module providing Prometheus and node_exporter, with dashboards left to ordinary declared apps.

> **Type:** explanation · **Audience:** operator · **Last reviewed:** 2026-06-11

> The platform bundles **no** observability by default ("declare Grafana
> as an app if you want it"). It keeps that principle by making observability
> a first-class module that is **off by default** — nothing runs unless you turn
> it on. Dashboards (Grafana) are still an ordinary declared app.

## What it gives you

When enabled, `modules/observability.nix` runs, on every host:

- a **node_exporter** publishing host metrics, and
- a **Prometheus** scraping that node_exporter plus `control-api`'s `/metrics`.

Both bind to **loopback** by default, exactly like `control-api` — there is no
firewall port opened. You reach them over the tailnet through the existing
front, or by an SSH tunnel.

`control-api` reports the state on `GET /v1/system`:

```json
{ "observability": { "enabled": true, "internal": true } }
```

The detailed observability state (metrics roll-up, apps, infra) comes from
`GET /v1/observability`, and the web **Monitoring** screen reflects whether it
is enabled.

## Enabling it

In `config/platform.nix` (or a per-host `hosts/<name>/platform.nix` overlay):

```nix
observability = {
  enable = true;                       # the master switch (default false)
  nodeExporter = { enable = true; port = 9100; listenAddress = "127.0.0.1"; };
  prometheus = {
    enable = true;
    port = 9090;
    retention = "15d";
    scrapeInterval = "15s";
    extraTargets = [ ];                # other hosts' node_exporters
  };
};
```

Rebuild the host (`nixos-rebuild switch --flake .#<host> --impure`) and
Prometheus comes up on `127.0.0.1:9090`.

## Scraping a whole fleet

Pick one host as the Prometheus host. On the **other** hosts, bind their
node_exporter to the tailnet address so the Prometheus host can reach it:

```nix
# hosts/edge/platform.nix
observability.nodeExporter.listenAddress = "100.x.y.z";  # edge's tailscale IP
observability.prometheus.enable = false;                  # edge has no Prometheus
```

```nix
# hosts/homelab/platform.nix  (the Prometheus host)
observability.prometheus.extraTargets = [ "100.x.y.z:9100" ];  # edge
```

When `nodeExporter.listenAddress` is set to a non-loopback address, the module
opens that port on the `tailscale0` interface only (so the fleet Prometheus can
reach it); the loopback default opens nothing. Each scrape is labelled with
`host=<name>` so series stay separable.

## Dashboards

This module ships metrics, not dashboards. If you want Grafana, declare it as a
normal app under `apps/` pointed at `http://127.0.0.1:9090` — it is not a
platform dependency.
