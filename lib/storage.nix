# Storage path resolver (HomeLab Platform V2).
#
# Turns a logical {class, app, volume} into a concrete host path using the
# storage classes declared in config/platform.nix. Example:
#   resolve { class = "nas"; app = "jellyfin"; volume = "config"; }
#   -> "/mnt/homelab/jellyfin/config"
{ lib, platform }:

let
  classes = platform.storageClasses;
in
{
  inherit classes;

  resolve = { class, app, volume }:
    let c = classes.${class} or (throw "unknown storage class '${class}'");
    in "${c.basePath}/${app}/${volume}";

  classBackedUp = class:
    let c = classes.${class} or null;
    in if c == null then false else (c.backedUp or false);

  # Optional per-class restic repository override. Empty string means "use the
  # global platform.backup.repository". Set via the control-plane storage-class
  # form (backupRepo attr) or directly in config/platform.nix.
  classBackupRepo = class:
    let c = classes.${class} or null;
    in if c == null then "" else (c.backupRepo or "");

  isKnownClass = class: builtins.hasAttr class classes;
}
