# Declarative backups (HomeLab Platform V2).
#
# Generates one restic backup service+timer per app that declares a backed-up
# volume. Inert by default: until platform.backup.repository is set the module
# adds nothing, so importing it changes no runtime behavior.
{ config, lib, pkgs, hostName ? "homelab", ... }:

let
  appsDir = ../apps;
  platform = import ../lib/load-platform.nix { inherit lib hostName; };
  policies = import ../lib/load-policies.nix { inherit lib hostName; };
  appModel = import ../lib/app-model.nix { inherit lib platform; };
  storage = import ../lib/storage.nix { inherit lib platform; };

  backup = platform.backup or { };
  enabled = (backup.repository or "") != "";
  retention = backup.retention or { daily = 7; weekly = 4; monthly = 6; };

  # restic forget flags from the declared retention policy. Pruning runs in the
  # same invocation so the repository never grows unbounded (v0.4 B3).
  forgetFlags = lib.concatStringsSep " " [
    "--keep-daily ${toString (retention.daily or 7)}"
    "--keep-weekly ${toString (retention.weekly or 4)}"
    "--keep-monthly ${toString (retention.monthly or 6)}"
  ];

  # An app's criticality decides whether a restore-test is mandatory.
  backupByCrit = policies.backupByCriticality or { };
  restoreTested = name: a:
    let r = backupByCrit.${a.criticality or "low"} or { };
    in (r.restoreTest or false);

  entries =
    if builtins.pathExists appsDir then
      lib.filterAttrs (n: t: t == "regular" && lib.hasSuffix ".nix" n && n != "default.nix")
        (builtins.readDir appsDir)
    else { };

  apps = lib.mapAttrs'
    (file: _:
      let name = lib.removeSuffix ".nix" file;
      in lib.nameValuePair name (appModel.normalize (import (appsDir + "/${file}"))))
    entries;

  backedUpVols = a: lib.filter (v: storage.classBackedUp v.class) a.volumes;

  # Effective restic repository for one volume: the class's backupRepo override
  # if set, otherwise the global platform.backup.repository. `enabled` guarantees
  # the global repo is non-empty, so this is never "".
  effectiveRepo = v:
    let r = storage.classBackupRepo v.class;
    in if r != "" then r else backup.repository;

  # Group an app's backed-up volume paths by their effective repository. Normally
  # one group (the global repo) → identical to single-repo behaviour; multiple
  # only when a class overrides backupRepo.
  repoGroups = name: a:
    let
      vols = backedUpVols a;
      repos = lib.unique (map effectiveRepo vols);
    in
    map
      (repo: {
        inherit repo;
        paths = map (v: storage.resolve { class = v.class; app = name; volume = v.name; })
          (lib.filter (v: effectiveRepo v == repo) vols);
      })
      repos;

  appsWithBackup = lib.filterAttrs (n: a: (backedUpVols a) != [ ]) apps;

  passwordFile = "/run/secrets/restic_password";

  scheduleCalendar =
    let s = backup.schedule or "daily";
    in if s == "hourly" then "*-*-* *:00:00"
    else if s == "weekly" then "Mon *-*-* 03:00:00"
    else "*-*-* 03:00:00"; # daily default

  # One restic invocation per distinct repository the app's volumes target. Each
  # backs up its paths, then forget+prunes to the retention policy in the same
  # run so every repository stays bounded. A prune failure does not fail the
  # backup; a backup failure preserves a non-zero exit.
  mkGroupBlock = name: g: ''
    export RESTIC_REPOSITORY=${lib.escapeShellArg g.repo}
    ${pkgs.restic}/bin/restic backup --tag ${lib.escapeShellArg name} \
      ${lib.concatStringsSep " " (map lib.escapeShellArg g.paths)} || rc=$?
    ${pkgs.restic}/bin/restic forget --tag ${lib.escapeShellArg name} ${forgetFlags} --prune \
      || echo "warn: forget/prune failed for ${name} (${g.repo})"
  '';

  mkBackupService = name: a: {
    description = "restic backup for ${name}";
    onFailure = [ "alert@%n.service" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = pkgs.writeShellScript "backup-${name}" ''
        set -uo pipefail
        rc=0
        ${lib.concatMapStringsSep "\n" (g: mkGroupBlock name g) (repoGroups name a)}
        exit $rc
      '';
    };
    environment = {
      RESTIC_PASSWORD_FILE = passwordFile;
    };
    path = [ pkgs.restic ];
  };

  # Restore-test (v0.4 B2): restore the latest snapshot for the app's tag into a
  # throwaway target, assert it is non-empty, record the result, and clean up.
  # Scheduled monthly for apps whose criticality requires a restore-tested
  # backup; the result line feeds control-api's backup coverage view.
  mkRestoreTestService = name: _: {
    description = "restic restore-test for ${name} (${hostName})";
    onFailure = [ "alert@%n.service" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = pkgs.writeShellScript "restore-test-${name}" ''
        set -uo pipefail
        export RESTIC_REPOSITORY=${lib.escapeShellArg backup.repository}
        export RESTIC_PASSWORD_FILE=${passwordFile}
        target="$(mktemp -d /tmp/hl-restore-test-${name}.XXXXXX)"
        trap 'rm -rf "$target"' EXIT
        mkdir -p /var/lib/homelab
        if ${pkgs.restic}/bin/restic restore latest --tag ${lib.escapeShellArg name} --target "$target" \
           && [ -n "$(find "$target" -type f -print -quit)" ]; then res=ok; else res=failed; fi
        printf '{"time":"%s","host":"%s","app":"%s","kind":"restore-test","result":"%s"}\n' \
          "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${hostName}" "${name}" "$res" \
          >> /var/lib/homelab/backup-results.jsonl
        [ "$res" = ok ]
      '';
    };
    path = [ pkgs.restic ];
  };

  mkRestoreTestTimer = name: _: {
    description = "schedule restic restore-test for ${name}";
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnCalendar = "*-*-01 05:00:00"; # monthly, after nightly backups + verify
      Persistent = true;
      RandomizedDelaySec = "30m";
    };
  };

  appsRestoreTested = lib.filterAttrs (n: a: restoreTested n a) appsWithBackup;

  mkBackupTimer = name: _: {
    description = "schedule restic backup for ${name}";
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnCalendar = scheduleCalendar;
      Persistent = true;
      RandomizedDelaySec = "15m";
    };
  };

  # Integrity verify: `restic check` validates the repository structure and a
  # rolling 5% data subset every run, so a silently corrupting backend is caught
  # without the cost of a full restore. Records a result line control-api reads.
  verifyService = {
    description = "verify restic repository (${hostName})";
    onFailure = [ "alert@%n.service" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = pkgs.writeShellScript "backup-verify" ''
        set -uo pipefail
        export RESTIC_REPOSITORY=${lib.escapeShellArg backup.repository}
        export RESTIC_PASSWORD_FILE=${passwordFile}
        mkdir -p /var/lib/homelab
        if ${pkgs.restic}/bin/restic check --read-data-subset=5%; then res=ok; else res=failed; fi
        printf '{"time":"%s","host":"%s","kind":"verify","result":"%s"}\n' \
          "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${hostName}" "$res" \
          >> /var/lib/homelab/backup-results.jsonl
        [ "$res" = ok ]
      '';
    };
    path = [ pkgs.restic ];
  };

  verifyTimer = {
    description = "schedule restic repository verify";
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnCalendar = "Sun *-*-* 04:00:00"; # weekly, after the nightly backups
      Persistent = true;
      RandomizedDelaySec = "30m";
    };
  };
in
lib.mkIf enabled {
  environment.systemPackages = [ pkgs.restic ];

  systemd.services = (lib.mapAttrs'
    (name: a: lib.nameValuePair "backup-${name}" (mkBackupService name a))
    appsWithBackup)
  // (lib.mapAttrs'
    (name: a: lib.nameValuePair "restore-test-${name}" (mkRestoreTestService name a))
    appsRestoreTested)
  // { backup-verify = verifyService; };

  systemd.timers = (lib.mapAttrs'
    (name: a: lib.nameValuePair "backup-${name}" (mkBackupTimer name a))
    appsWithBackup)
  // (lib.mapAttrs'
    (name: a: lib.nameValuePair "restore-test-${name}" (mkRestoreTestTimer name a))
    appsRestoreTested)
  // { backup-verify = verifyTimer; };
}
