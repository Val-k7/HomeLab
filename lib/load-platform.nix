# Merge the shared platform base (config/platform.nix) with an optional
# per-host overlay (hosts/<name>/platform.nix). Used by modules/platform.nix
# (validation + /etc/homelab/platform.json) and modules/observability.nix so
# both see the same effective config. host.hostname always tracks the flake
# host name unless the overlay sets it explicitly.
{ lib, hostName ? "homelab" }:

let
  base = import ../config/platform.nix;
  overlayFile = ../hosts + "/${hostName}/platform.nix";
  overlay = if builtins.pathExists overlayFile then import overlayFile else { };
in
lib.recursiveUpdate
  (lib.recursiveUpdate base { host.hostname = hostName; })
  overlay
