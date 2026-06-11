# Eval-time tests for the storage path resolver.
{ lib }:

let
  platform = import ../config/platform.nix;
  s = import ../lib/storage.nix { inherit lib platform; };

  checks = [
    {
      name = "resolve-nas";
      ok = s.resolve { class = "nas"; app = "jellyfin"; volume = "config"; }
        == "/mnt/homelab/jellyfin/config";
    }
    { name = "local-backedUp"; ok = s.classBackedUp "local" == true; }
    { name = "cache-not-backedUp"; ok = s.classBackedUp "cache" == false; }
    { name = "bulk-backedUp"; ok = s.classBackedUp "bulk" == true; }
    { name = "ephemeral-not-backedUp"; ok = s.classBackedUp "ephemeral" == false; }
    { name = "known-class"; ok = s.isKnownClass "fast" == true; }
    { name = "known-bulk"; ok = s.isKnownClass "bulk" == true; }
    { name = "known-ephemeral"; ok = s.isKnownClass "ephemeral" == true; }
    { name = "unknown-class"; ok = s.isKnownClass "nope" == false; }
    { name = "resolve-bulk"; ok = s.resolve { class = "bulk"; app = "a"; volume = "v"; } == "/mnt/bulk/homelab/a/v"; }
  ];

  failed = builtins.filter (c: !c.ok) checks;
in
if failed == [ ] then "ok"
else throw "storage test failures: ${builtins.toJSON (map (c: c.name) failed)}"
