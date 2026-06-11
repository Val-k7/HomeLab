{ config, lib, pkgs, hostName ? "homelab", ... }:

let
  appsDir = ../apps;
  # Use the per-host merged platform so a hosts/<name>/platform.nix overlay
  # (e.g. different storage classes) flows into app volume resolution.
  platform = import ../lib/load-platform.nix { inherit lib hostName; };
  appModel = import ../lib/app-model.nix { inherit lib platform; };
  storage = import ../lib/storage.nix { inherit lib platform; };

  entries =
    if builtins.pathExists appsDir then
      lib.filterAttrs
        (n: t: t == "regular" && lib.hasSuffix ".nix" n && n != "default.nix")
        (builtins.readDir appsDir)
    else { };

  # Normalize every app (v1 or v2) to the single internal model.
  apps = lib.mapAttrs'
    (file: _:
      let name = lib.removeSuffix ".nix" file;
      in lib.nameValuePair name (appModel.normalize (import (appsDir + "/${file}"))))
    entries;

  resolveVolume = name: v: storage.resolve { class = v.class; app = name; volume = v.name; };

  optEnvFile = a: lib.optionalAttrs (a.envFile != null) { EnvironmentFile = a.envFile; };

  # Bind-mounted volume args for docker runners (only volumes declaring a
  # container path). Host dirs are created by systemd.tmpfiles below.
  dockerVolArgs = name: a:
    lib.concatStringsSep " "
      (map (v: "-v ${resolveVolume name v}:${v.path}")
        (lib.filter (v: v.path != null) a.volumes));

  hostVolumePaths = name: a: map (v: resolveVolume name v) a.volumes;

  processService = name: a:
    let
      home = "/var/lib/app-${name}";
      runtimePkgs = [ pkgs.${a.runtime} pkgs.git pkgs.bash pkgs.cacert ]
        ++ map (p: pkgs.${p}) (a.packages or [ ]);
      preStart = pkgs.writeShellScript "app-${name}-prestart" ''
        set -eu
        cd ${home}
        if [ ! -d src/.git ]; then git clone ${a.repo} src; fi
        cd src
        git fetch origin
        git checkout -q ${a.rev}
        if [ "$(cat ${home}/.built-rev 2>/dev/null || true)" != "${a.rev}" ]; then
          ${a.buildCmd}
          echo ${a.rev} > ${home}/.built-rev
        fi
      '';
      startScript = pkgs.writeShellScript "app-${name}-start" ''
        exec ${a.startCmd}
      '';
    in
    {
      description = "app ${name}";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];
      path = runtimePkgs;
      environment = { SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"; } // (a.env or { });
      serviceConfig = {
        Type = "simple";
        StateDirectory = "app-${name}";
        WorkingDirectory = "${home}/src";
        DynamicUser = true;
        ExecStartPre = "${preStart}";
        ExecStart = "${startScript}";
        Restart = "on-failure";
        RestartSec = 5;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        NoNewPrivileges = true;
        ReadWritePaths = [ home ] ++ (hostVolumePaths name a);
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" ];
      } // optEnvFile a;
    };

  composeFile = a: "${a.dir}/docker-compose.yml";

  # Minimal hardening for the docker-CLI runners (image/compose/dockerfile).
  # These units only drive the docker CLI, which talks to dockerd over its unix
  # socket — and socket access already means full control of the daemon, so
  # sandboxing the *client* buys almost nothing. The real isolation lives in the
  # container (docker run security opts) and the container->host firewall
  # (homelab.network.isolateContainers), not here. We deliberately do NOT set
  # ProtectHome/ProtectSystem=strict/PrivateTmp/CapabilityBoundingSet: they break
  # `docker compose` plugin discovery (reads /root/.docker), `docker build`
  # (needs /tmp and caps), and config loading. NoNewPrivileges is the one knob
  # that constrains the client without breaking it.
  dockerCliHardening = {
    NoNewPrivileges = true;
  };

  imageService = name: a:
    let
      imageRef =
        if a.digest != "" then "${a.image}@${a.digest}"
        else if lib.hasPrefix "sha256:" a.tag then "${a.image}@${a.tag}"
        else if lib.hasPrefix "@" a.tag then "${a.image}${a.tag}"
        else "${a.image}:${a.tag}";
      container = "app-${name}";
      # Host port maps to the image's internal port when declared (whoami
      # listens on 80 regardless of the host port the operator picked).
      innerPort = if (a.containerPort or 0) > 0 then a.containerPort else a.port;
      portArg = lib.optionalString (a.port > 0) "-p ${toString a.port}:${toString innerPort}";
      volArg = dockerVolArgs name a;
      envArgs = lib.concatStringsSep " "
        (lib.mapAttrsToList (k: v: "-e ${k}=${lib.escapeShellArg v}") (a.env or { }));
    in
    {
      description = "app ${name}";
      wantedBy = [ "multi-user.target" ];
      requires = [ "docker.service" ];
      after = [ "docker.service" ];
      path = [ pkgs.docker_29 ];
      serviceConfig = {
        Type = "simple";
        ExecStartPre = "-${pkgs.docker_29}/bin/docker rm -f ${container}";
        ExecStart = "${pkgs.docker_29}/bin/docker run --rm --name ${container} ${portArg} ${volArg} ${envArgs} ${imageRef}";
        ExecStop = "-${pkgs.docker_29}/bin/docker stop ${container}";
        Restart = "on-failure";
        RestartSec = 5;
      } // dockerCliHardening // optEnvFile a;
    };

  composeService = name: a:
    {
      description = "app ${name}";
      wantedBy = [ "multi-user.target" ];
      requires = [ "docker.service" ];
      after = [ "docker.service" ];
      path = [ pkgs.docker_29 ];
      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
        ExecStart = "${pkgs.docker_29}/bin/docker compose -f ${composeFile a} up -d";
        ExecStop = "${pkgs.docker_29}/bin/docker compose -f ${composeFile a} down";
      } // dockerCliHardening // optEnvFile a;
    };

  dockerfileService = name: a:
    let
      home = "/var/lib/app-${name}";
      image = "app-${name}";
      preStart = pkgs.writeShellScript "app-${name}-prestart" ''
        set -eu
        export SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt
        cd ${home}
        if [ ! -d src/.git ]; then ${pkgs.git}/bin/git clone ${a.repo} src; fi
        cd src
        ${pkgs.git}/bin/git fetch origin
        ${pkgs.git}/bin/git checkout -q ${a.rev}
        ${pkgs.docker_29}/bin/docker build -t ${image}:${a.rev} .
      '';
      innerPort = if (a.containerPort or 0) > 0 then a.containerPort else a.port;
      portArg = lib.optionalString (a.port > 0) "-p ${toString a.port}:${toString innerPort}";
      volArg = dockerVolArgs name a;
    in
    {
      description = "app ${name}";
      wantedBy = [ "multi-user.target" ];
      requires = [ "docker.service" ];
      after = [ "docker.service" ];
      path = [ pkgs.git pkgs.docker_29 ];
      serviceConfig = {
        Type = "simple";
        StateDirectory = "app-${name}";
        ExecStartPre = "${preStart}";
        ExecStart = "${pkgs.docker_29}/bin/docker run --rm --name ${image} ${portArg} ${volArg} ${image}:${a.rev}";
        ExecStop = "${pkgs.docker_29}/bin/docker stop ${image}";
        Restart = "on-failure";
        RestartSec = 5;
      } // dockerCliHardening // {
        # The prestart git clone/checkout writes the app source under the
        # state dir, which ProtectSystem=strict would otherwise make read-only.
        ReadWritePaths = [ home ];
      } // optEnvFile a;
    };

  # v2 `nixos` runner: the app supplies a systemd service body directly via
  # `module.systemdService`. Preferred when a native NixOS path exists.
  nixosService = name: a:
    let
      m = a.module;
      svc = if m == null then { } else (m.systemdService or { });
    in
    {
      description = "app ${name}";
      wantedBy = [ "multi-user.target" ];
    } // svc;

  # Inter-app ordering. An app's declared dependencies (other app names) become
  # systemd `after` + `wants` on app-<dep>.service, so a dependency starts (and
  # is attempted) before its dependents. `wants` (not `requires`) keeps it a
  # soft order: a failing dependency does not tear the dependent down, matching
  # the rest of the app units which self-heal via Restart=on-failure.
  depUnits = a: map (d: "app-${d}.service") a.dependencies;

  withDeps = a: svc:
    svc // {
      after = (svc.after or [ ]) ++ depUnits a;
      wants = (svc.wants or [ ]) ++ depUnits a;
    };

  baseService = name: a:
    if a.runner == "image" then imageService name a
    else if a.runner == "process" then processService name a
    else if a.runner == "compose" then composeService name a
    else if a.runner == "dockerfile" then dockerfileService name a
    else if a.runner == "nixos" then nixosService name a
    else throw "app ${name}: unknown runner '${a.runner}'";

  mkService = name: a: withDeps a (baseService name a) // { onFailure = [ "alert@%n.service" ]; };

  # Validate the dependency graph at eval time: every dependency must name a
  # real app and an app may not depend on itself (a self-loop systemd ignores).
  appNames = lib.attrNames apps;
  unknownDeps = lib.flatten (lib.mapAttrsToList
    (n: a: map (d: "${n} -> ${d}") (lib.filter (d: !(lib.elem d appNames)) a.dependencies))
    apps);
  selfDeps = lib.attrNames (lib.filterAttrs (n: a: lib.elem n a.dependencies) apps);

  # Enriched desired-state manifest. Never includes secret values — only the
  # declared secret names and their required flag.
  manifest = lib.mapAttrs
    (n: a: {
      schemaVersion = a.schemaVersion;
      runner = a.runner;
      source = a.source;
      image = a.image;
      tag = a.tag;
      digest = a.digest;
      repo = a.repo;
      rev = a.rev;
      hash = a.hash;
      port = a.port;
      ports = a.ports;
      metrics = a.metrics;
      metricsPath = a.metricsPath;
      updatePolicy = a.updatePolicy;
      criticality = a.criticality;
      permissions = a.permissions;
      volumes = map
        (v: {
          inherit (v) name kind class;
          path = resolveVolume n v;
          backedUp = storage.classBackedUp v.class;
        })
        a.volumes;
      secrets = map (s: { inherit (s) name required; }) a.secrets;
      healthcheck = a.healthcheck;
      dependencies = a.dependencies;
    })
    apps;

  appPorts = lib.filter (p: p > 0)
    (lib.mapAttrsToList (_: a: a.port) apps);

  # Create host directories for every declared volume.
  volumeTmpfiles = lib.flatten
    (lib.mapAttrsToList
      (n: a: map (v: "d ${resolveVolume n v} 0750 root root -") a.volumes)
      apps);
in
{
  assertions = [
    {
      assertion = unknownDeps == [ ];
      message = "apps: dependency on unknown app(s): ${lib.concatStringsSep ", " unknownDeps}";
    }
    {
      assertion = selfDeps == [ ];
      message = "apps: app(s) depend on themselves: ${lib.concatStringsSep ", " selfDeps}";
    }
  ];

  systemd.services =
    lib.mapAttrs' (name: a: lib.nameValuePair "app-${name}" (mkService name a)) apps;

  systemd.tmpfiles.rules = volumeTmpfiles;

  environment.etc."homelab/apps.json".text = builtins.toJSON manifest;

  networking.firewall.interfaces.tailscale0.allowedTCPPorts = appPorts;
}
