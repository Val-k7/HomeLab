# Restore end-to-end gate (roadmap v0.6 T2).
#
# NixOS VM test proving the backup -> restore cycle actually round-trips:
#   1. seed a dummy app state dir with a known file,
#   2. restic backup + forget/prune with the exact tag/retention flags
#      modules/backup.nix generates (mkGroupBlock),
#   3. assert the snapshot exists, destroy the state, restore it, assert the
#      original content is byte-identical,
#   4. restore-test pattern from modules/backup.nix (restore latest into a
#      throwaway target, assert non-empty),
#   5. `restic check --read-data-subset=5%` (same verify as backup-verify),
#   6. drive the bin/backup.sh wrapper verbs (backup / snapshots /
#      restore-test / restore / verify) against the same repository and
#      assert the backups.json state it writes for control-api.
#
# NOT covered (known limitation): the per-app systemd units modules/backup.nix
# generates. They require a v2 app with backed-up volumes in apps/, and apps/
# is global to every host, so the module cannot target a throwaway app without
# deploying it fleet-wide. The restic command lines below are kept in lockstep
# with modules/backup.nix by construction — update both together.
#
# Heavy check: needs build + KVM, so checks.yml runs it in a dedicated
# restore-e2e job (and release.yml gates tag releases on it) instead of the
# default `nix flake check --no-build` path.
{ pkgs, lib }:

pkgs.testers.runNixOSTest {
  name = "restore-e2e";

  nodes.machine = { pkgs, ... }: {
    environment.systemPackages = [ pkgs.restic ];
    # The runtime wrapper under test, byte-identical to the repo copy.
    environment.etc."homelab-test/backup.sh".source = ../bin/backup.sh;
    virtualisation.memorySize = 1024;
  };

  testScript = ''
    machine.wait_for_unit("multi-user.target")

    repo = "/var/backup/restic-repo"
    state = "/var/lib/homelab/data/dummy/state"
    env = f"RESTIC_REPOSITORY={repo} RESTIC_PASSWORD_FILE=/root/restic-pass"
    sentinel = "hello-homelab-restore-e2e"

    machine.succeed("mkdir -p /var/backup")
    machine.succeed("echo test-password > /root/restic-pass")
    machine.succeed(f"mkdir -p {state}")
    machine.succeed(f"echo '{sentinel}' > {state}/app.dat")
    machine.succeed(f"{env} restic init")

    with subtest("backup with the module's exact flags (mkGroupBlock)"):
        machine.succeed(f"{env} restic backup --tag dummy {state}")
        machine.succeed(
            f"{env} restic forget --tag dummy"
            " --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune"
        )
        out = machine.succeed(f"{env} restic snapshots --tag dummy")
        assert "dummy" in out, f"expected a tagged snapshot, got: {out}"

    with subtest("destroy state, restore, content is back"):
        machine.succeed(f"rm -rf {state}")
        machine.succeed(f"{env} restic restore latest --tag dummy --target /")
        content = machine.succeed(f"cat {state}/app.dat").strip()
        assert content == sentinel, f"restored content wrong: {content!r}"

    with subtest("restore-test pattern (modules/backup.nix mkRestoreTestService)"):
        machine.succeed(
            'target="$(mktemp -d /tmp/hl-restore-test.XXXXXX)" && '
            f"{env} restic restore latest --tag dummy --target \"$target\" && "
            '[ -n "$(find "$target" -type f -print -quit)" ] && rm -rf "$target"'
        )

    with subtest("repository integrity (backup-verify flags)"):
        machine.succeed(f"{env} restic check --read-data-subset=5%")

    # bin/backup.sh wrapper: same repo via env, state dir under /var/lib/homelab.
    wrap = f"HOMELAB_STATE_DIR=/var/lib/homelab {env} bash /etc/homelab-test/backup.sh"

    with subtest("bin/backup.sh backup + snapshots"):
        machine.succeed(f"{wrap} backup dummy")
        machine.succeed("grep -q last_backup /var/lib/homelab/backups.json")
        out = machine.succeed(f"{wrap} snapshots")
        assert "dummy" in out, f"wrapper snapshot missing: {out}"

    with subtest("bin/backup.sh restore-test + restore + verify"):
        machine.succeed(f"{wrap} restore-test dummy")
        machine.succeed("grep -q last_restore_test /var/lib/homelab/backups.json")
        machine.succeed(f"{wrap} restore dummy")
        machine.succeed(
            '[ -n "$(find /var/lib/homelab/restore-tmp -type f -print -quit)" ]'
        )
        machine.succeed(f"{wrap} verify")
  '';
}
