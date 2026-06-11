# App model normalizer (HomeLab Platform V2).
#
# Maps both v1 apps (`{ runner, image/tag, repo/rev, dir, port, metrics }`)
# and v2 apps (`{ schemaVersion = 2; runtime = {...}; criticality; ... }`)
# onto a single internal model so the rest of the system only ever sees one
# shape. v1 apps keep their exact runtime behavior; the extra v2 fields are
# filled with safe defaults derived from the platform config.
{ lib, platform }:

let
  defaultUpdatePolicy = platform.updatePolicyDefault or "manual";
  defaultStorageClass = platform.defaultStorageClass or "local";

  base = {
    schemaVersion = 1;
    source = "local";
    runner = "image";
    image = "";
    tag = "";
    digest = "";
    repo = "";
    rev = "";
    hash = "";
    runtime = "";
    buildCmd = "";
    startCmd = "";
    dir = null;
    packages = [ ];
    env = { };
    envFile = null;
    port = 0;
    # Container-side port for docker runners. 0 = same as `port` (host port).
    # Needed when the image listens on a fixed internal port (e.g. whoami on 80)
    # while the host port is chosen by the operator.
    containerPort = 0;
    ports = [ ];
    metrics = false;
    metricsPath = "/metrics";
    updatePolicy = defaultUpdatePolicy;
    criticality = "low";
    permissions = [ ];
    volumes = [ ];
    secrets = [ ];
    healthcheck = null;
    dependencies = [ ];
    module = null;
  };

  # Read raw.metrics whether it is a bool (v1) or a set (v2: { enabled; path; }).
  metricsEnabled = raw:
    let m = raw.metrics or false;
    in if builtins.isBool m then m else (m.enabled or false);
  metricsPathOf = raw:
    let m = raw.metrics or null;
    in if builtins.isAttrs m then (m.path or "/metrics") else (raw.metricsPath or "/metrics");

  normalizeVolume = v: {
    name = v.name;
    kind = v.kind or "data";
    class = v.class or defaultStorageClass;
  };

  normalizeSecret = s: {
    name = s.name;
    required = s.required or true;
    mountPath = s.mountPath or null;
  };

  normalizeV1 = raw: base // {
    schemaVersion = 1;
    runner = raw.runner;
    image = raw.image or "";
    tag = raw.tag or "";
    digest = raw.digest or "";
    repo = raw.repo or "";
    rev = raw.rev or "";
    runtime = raw.runtime or "";
    buildCmd = raw.buildCmd or "";
    startCmd = raw.startCmd or "";
    dir = raw.dir or null;
    packages = raw.packages or [ ];
    env = raw.env or { };
    envFile = raw.envFile or null;
    port = raw.port or 0;
    containerPort = raw.containerPort or 0;
    metrics = metricsEnabled raw;
    metricsPath = metricsPathOf raw;
    updatePolicy = raw.updatePolicy or defaultUpdatePolicy;
    # Infer the minimal permission set from what a v1 app actually uses so the
    # policy engine has something to check without forcing a migration.
    permissions = (lib.optional ((raw.port or 0) > 0) "tailnet-port")
      ++ (lib.optional (metricsEnabled raw) "metrics")
      ++ (lib.optional (lib.elem (raw.runner) [ "compose" "dockerfile" "image" ]) "docker");
  };

  normalizeV2 = raw:
    let rt = raw.runtime or { };
    in base // {
      schemaVersion = 2;
      source = raw.source or "local";
      runner = rt.runner or raw.runner or "image";
      image = rt.image or "";
      tag = rt.tag or "";
      digest = rt.digest or "";
      repo = rt.repo or "";
      rev = rt.rev or "";
      hash = rt.hash or "";
      runtime = rt.runtime or "";
      buildCmd = rt.buildCmd or "";
      startCmd = rt.startCmd or "";
      dir = rt.dir or null;
      packages = rt.packages or [ ];
      env = raw.env or { };
      envFile = raw.envFile or null;
      ports = rt.ports or [ ];
      port = rt.port or (if (rt.ports or [ ]) != [ ] then builtins.head (rt.ports) else 0);
      containerPort = rt.containerPort or 0;
      metrics = metricsEnabled raw;
      metricsPath = metricsPathOf raw;
      updatePolicy = raw.updatePolicy or defaultUpdatePolicy;
      criticality = raw.criticality or "low";
      permissions = raw.permissions or [ ];
      volumes = map normalizeVolume (raw.volumes or [ ]);
      secrets = map normalizeSecret (raw.secrets or [ ]);
      healthcheck = raw.healthcheck or null;
      dependencies = raw.dependencies or [ ];
      module = raw.module or null;
    };

  normalize = raw:
    if (raw.schemaVersion or 1) >= 2 then normalizeV2 raw else normalizeV1 raw;
in
{
  inherit normalize;
}
