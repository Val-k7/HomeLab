# Merge the shared policy base (config/policies.nix) with an optional per-host
# overlay (hosts/<name>/policies.nix). Mirrors lib/load-platform.nix so a host
# can soften or harden the global posture — e.g. a host can stay in warn mode
# (`strict = false`) during a migration while the fleet default is strict.
# Used by modules/platform.nix (/etc/homelab/policies.json) and the policy
# engine that reads it.
{ lib, hostName ? "homelab" }:

let
  base = import ../config/policies.nix;
  overlayFile = ../hosts + "/${hostName}/policies.nix";
  overlay = if builtins.pathExists overlayFile then import overlayFile else { };
in
lib.recursiveUpdate base overlay
