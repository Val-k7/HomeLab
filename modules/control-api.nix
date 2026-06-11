{ config, lib, pkgs, env, inputs ? { }, hostName ? "homelab", ... }:

let
  e = import ../lib/env-lib.nix { inherit lib env; };
  port = e.getInt "CONTROL_API_PORT" 9092;
  username = e.get "USERNAME" "admin";
  homelabDir = "/home/${username}/homelab";
  repoUrl = e.get "REPO_URL" "";
  accessFile = ../config/access.json;
  dockerProxyPort = 2375;
  # Stamp the binary with the deployed commit (flake self, passed in via
  # specialArgs.inputs). shortRev is only set on a CLEAN tree; a dirty rebuild
  # (the normal git-pull-then-rebuild flow can leave the worktree dirty) drops
  # it, which silently stamped "dev" in prod. Fall back to dirtyShortRev (set
  # when the tree IS dirty) before the Go default "dev"
  # (control-api/main.go: var version = "dev").
  rev = inputs.self.shortRev or inputs.self.dirtyShortRev or "dev";
  pkg = pkgs.buildGoModule {
    pname = "control-api";
    version = "0.1.0";
    src = ../control-api;
    vendorHash = null;
    ldflags = [ "-X main.version=${rev}" ];
  };
  # React control-plane bundle. npmDepsHash must match web/package-lock.json:
  # on the first `nix build` of this, Nix prints the correct sha256 to paste
  # here (standard buildNpmPackage bootstrap). CI verifies the npm build
  # independently via the `web` job.
  webPkg = pkgs.buildNpmPackage {
    pname = "homelab-ui";
    version = "0.1.0";
    src = ../web;
    npmDepsHash = "sha256-Slx/uFTY0FoY4SO76n3vTGwWPIzQuIr8G/iw+gEMMrE=";
    installPhase = ''
      runHook preInstall
      cp -r dist $out
      runHook postInstall
    '';
  };
  # World-readable copy of the repo SOURCE files the config-editing handlers
  # display (config/*.nix, apps/*.nix, .sops.yaml, workshop-lock.json). The
  # sandboxed control-api cannot read the operator's 0700 /home checkout, so it
  # reads from here via HOMELAB_SOURCE_DIR. This is the deployed commit's source,
  # which is exactly what the editor should show; mutations still PR against main.
  homelabSource = pkgs.runCommand "homelab-source" { } ''
    mkdir -p $out
    cp -r ${../config} $out/config
    cp -r ${../apps} $out/apps
    cp ${../.sops.yaml} $out/.sops.yaml
    cp ${../workshop-lock.json} $out/workshop-lock.json
    # Ciphertext only (already committed to the repo); lets the API read the
    # sops lastmodified stamps to report secret rotation age.
    cp -r ${../secrets} $out/secrets
  '';
in
{
  users.users.controlapi = {
    isSystemUser = true;
    group = "controlapi";
  };
  users.groups.controlapi = { };

  # PRIVILEGE MODEL
  # control-api runs fully sandboxed (NoNewPrivileges=true, empty bounding set)
  # and NEVER uses sudo. It performs privileged actions only by sending D-Bus
  # messages to systemd/logind, authorized by the polkit rule below:
  #   * deploy/backup jobs  -> `systemctl start hl-deploy@<id>` / `hl-backup@<id>`
  #     template units whose ExecStart is FIXED in Nix (the caller chooses only a
  #     job id; the command is not caller-controlled). Args are passed via a
  #     job-spec file under /var/lib/homelab/jobs that the run-script re-validates.
  #   * app service control -> `systemctl start|stop|restart app-*.service`
  #   * host reboot         -> `systemctl reboot`
  #   * docker engine bounce-> `systemctl restart docker.service`
  #   * container ops       -> the docker CLI talks to a read/start/stop-only
  #     docker-socket-proxy over TCP (DOCKER_HOST), never the raw socket.
  # There is intentionally NO sudo rule and NO docker group: a compromised
  # control-api can only start the fixed units above and use the filtered proxy.
  # Restrict the /etc copy to root + the controlapi group: the control-api
  # unit runs as controlapi and must be able to read the role mapping (0400
  # root:root locks it out and silently downgrades everyone to viewer). Note:
  # the source still transits the world-readable nix store (part of the flake
  # source), so this mode protects the /etc path but not the store copy.
  environment.etc."homelab/access.json" = {
    source = accessFile;
    mode = "0440";
    user = "root";
    group = "controlapi";
  };
  environment.etc."homelab/bin/hl-deploy-run" = { source = ../bin/hl-deploy-run; mode = "0755"; };
  environment.etc."homelab/bin/hl-backup-run" = { source = ../bin/hl-backup-run; mode = "0755"; };
  environment.etc."homelab/control.env".text = ''
    HOMELAB_DIR=${homelabDir}
    HOMELAB_HOST=${hostName}
  '';

  # Fixed-command template units. The instance (%i) is a job id; the spec lives in
  # a file the run-script re-validates. control-api can start these (polkit) but
  # cannot alter what they run.
  # path: the run-scripts use `#!/usr/bin/env bash` and call git/restic/nix;
  # systemd units get an empty PATH by default, so without this every job dies
  # instantly with `env: 'bash': No such file or directory` (exit 127).
  systemd.services."hl-deploy@" = {
    description = "homelab deploy job %i";
    path = [ pkgs.bash pkgs.coreutils pkgs.git pkgs.nix pkgs.systemd ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "/etc/homelab/bin/hl-deploy-run %i";
    };
  };
  systemd.services."hl-backup@" = {
    description = "homelab backup job %i";
    path = [ pkgs.bash pkgs.coreutils pkgs.restic pkgs.systemd ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "/etc/homelab/bin/hl-backup-run %i";
    };
  };

  # Authorize ONLY the controlapi user, ONLY for the units above (+ app services,
  # docker.service) and host reboot. Note the rule checks subject.user only (not
  # subject.active/local), because controlapi is a headless system service with no
  # login session.
  #
  # polkit.extraConfig is only honoured when polkit is enabled. On a headless
  # server nothing else pulls it in, so without this the rule below is silently
  # dropped and systemd denies the controlapi system user every privileged
  # action (app restart, hl-backup@/hl-deploy@ start, reboot) with "Access
  # denied". The whole privilege-delegation model depends on this being true.
  security.polkit.enable = true;
  security.polkit.extraConfig = ''
    polkit.addRule(function(action, subject) {
      if (subject.user !== "controlapi") { return polkit.Result.NOT_HANDLED; }
      if (action.id === "org.freedesktop.systemd1.manage-units") {
        var unit = action.lookup("unit") || "";
        if (/^hl-deploy@.+\.service$/.test(unit) ||
            /^hl-backup@.+\.service$/.test(unit) ||
            /^app-[a-z0-9-]+\.service$/.test(unit) ||
            unit === "docker.service" ||
            unit === "reboot.target") {
          return polkit.Result.YES;
        }
      }
      if (action.id === "org.freedesktop.login1.reboot" ||
          action.id === "org.freedesktop.login1.reboot-multiple-sessions") {
        return polkit.Result.YES;
      }
      return polkit.Result.NOT_HANDLED;
    });
  '';

  # Filtered docker access in front of the root-equivalent docker socket;
  # control-api points DOCKER_HOST at it, never at /run/docker.sock. The UI only
  # needs list/inspect (GET) + container restart, so we DENY all other writes:
  # POST=0 blocks /containers/create and /exec (the host-root escape — a created
  # container can bind-mount host /), and ALLOW_RESTARTS=1 re-permits only
  # POST /containers/{id}/restart. App start/stop go through systemd, not docker.
  virtualisation.oci-containers.backend = lib.mkDefault "docker";
  virtualisation.oci-containers.containers.docker-socket-proxy = {
    image = "ghcr.io/tecnativa/docker-socket-proxy:0.3.0";
    autoStart = true;
    ports = [ "127.0.0.1:${toString dockerProxyPort}:2375" ];
    volumes = [ "/run/docker.sock:/var/run/docker.sock:ro" ];
    environment = {
      CONTAINERS = "1";     # GET /containers (list + inspect)
      POST = "0";           # block create/exec/start/stop and every other write
      ALLOW_RESTARTS = "1"; # except POST /containers/{id}/restart
    };
  };

  systemd.services.control-api = {
    description = "homelab control api";
    wantedBy = [ "multi-user.target" ];
    after = [ "network.target" "docker.service" ];
    path = [ pkgs.docker_29 pkgs.git pkgs.gh pkgs.systemd pkgs.sops pkgs.restic ];
    environment = {
      CONTROL_API_ADDR = "127.0.0.1:${toString port}";
      WEB_ROOT = "${webPkg}";
      HOMELAB_DIR = homelabDir;
      HOMELAB_SOURCE_DIR = homelabSource;
      HOMELAB_HOST = hostName;
      REPO_URL = repoUrl;
      DOCKER_HOST = "tcp://127.0.0.1:${toString dockerProxyPort}";
      HOMELAB_ACCESS_FILE = "/etc/homelab/access.json";
      HOMELAB_PLATFORM_FILE = "/etc/homelab/platform.json";
      HOMELAB_POLICIES_FILE = "/etc/homelab/policies.json";
      HOMELAB_CATALOGS_FILE = "/etc/homelab/catalogs.json";
      HOMELAB_WORKSHOP_LOCK_FILE = "${homelabSource}/workshop-lock.json";
      # git_token is provisioned straight onto the host by the deploy workflow
      # (0440 root:controlapi), not through sops/age. readGitToken() reads this
      # path; ProtectSystem=strict still allows reads, and the controlapi group
      # grants access.
      HOMELAB_GIT_TOKEN_FILE = "/var/lib/homelab-secrets/git_token";
    };
    serviceConfig = {
      ExecStart = "${pkg}/bin/control-api";
      User = "controlapi";
      Group = "controlapi";
      # /v1/logs shells `journalctl -u app-<app>.service`. Without this group the
      # controlapi user cannot open the journal ("No journal files were opened
      # due to insufficient permissions"), so log viewing returns nothing.
      SupplementaryGroups = [ "systemd-journal" ];
      Restart = "on-failure";
      RestartSec = 5;
      StateDirectory = "homelab";
      StateDirectoryMode = "0755";
      # Fully masked: config-editing reads now come from HOMELAB_SOURCE_DIR (a
      # world-readable nix-store copy), so the service never needs the operator's
      # /home checkout. Keep the strongest sandbox.
      ProtectHome = true;
      PrivateTmp = true;
      # control-api never elevates itself: privileged work is delegated to systemd
      # via polkit (see the PRIVILEGE MODEL note above), so the unit can keep the
      # strong sandbox.
      NoNewPrivileges = true;
      ProtectSystem = "strict";
      ProtectKernelTunables = true;
      ProtectKernelModules = true;
      ProtectControlGroups = true;
      PrivateDevices = true;
      RestrictSUIDSGID = true;
      LockPersonality = true;
      RestrictRealtime = true;
      MemoryDenyWriteExecute = true;
      SystemCallArchitectures = "native";
      RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" ];
      CapabilityBoundingSet = "";
      UMask = "0077";
    };
  };

  # control-api listens on loopback only; it is reached through oauth2-proxy,
  # which tailscale serve exposes on the tailnet. No direct firewall port.
}
