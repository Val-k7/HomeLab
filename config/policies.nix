# Platform policy rules (HomeLab Platform V2).
#
# Consumed by modules/platform.nix (/etc/homelab/policies.json), by the Go
# policy engine (control-api/policy_engine.go) and by the CI validator
# (tools/validate-platform.go). Default posture is deny: a capability must be
# explicitly granted by an app permission to be allowed.
{
  # Capabilities refused unless an app explicitly requests the matching
  # permission. Keys map to permission names in the app model.
  forbidden = {
    privileged = true; # privileged-container
    hostRootMount = true; # host-root-mount
    dockerSocket = true; # docker-socket
    secretInline = true; # secrets must be SOPS, never inline in apps/*.nix
  };

  image = {
    # v0.3 "Lockdown": image apps must pin a digest, not just a tag.
    requireDigest = true;
    # `latest`/`stable`/`main`/`release` are update intentions, never runtime
    # versions. allowLatest=false forbids them as the deployed tag in strict.
    allowLatest = false;
    # Supply-chain allowlist: image apps may only pull from these registries.
    # A short Docker Hub name (e.g. `nginx`) resolves to `docker.io`. Empty
    # disables the check. Add a registry here via a reviewed PR.
    allowedRegistries = [ "docker.io" "ghcr.io" "quay.io" "registry.k8s.io" ];
  };

  secrets = {
    allowInline = false;
  };

  # Backup coverage requirements per criticality tier.
  backupByCriticality = {
    low = { required = false; restoreTest = false; };
    medium = { required = false; restoreTest = false; };
    high = { required = true; restoreTest = false; };
    critical = { required = true; restoreTest = true; };
  };

  ports = {
    allowPublic = false;
    reserved = [ 22 80 443 3000 3100 3200 4040 9090 9092 9100 9101 ];
  };

  update = {
    # Only these update policies may be auto-merged by CI.
    automergeAllowed = [ "autoLow" ];
    # Apps with a database volume never auto-merge unless explicitly allowed.
    databaseBlocksAutomerge = true;
  };

  # Permissions an app may declare. The policy engine rejects any unknown
  # permission and any forbidden capability used without its permission.
  knownPermissions = [
    "docker"
    "tailnet-port"
    "public-port"
    "persistent-storage"
    "secret-access"
    "metrics"
    "privileged-container"
    "host-root-mount"
    "docker-socket"
  ];

  # Strict mode (v0.3 "Lockdown", default ON). Turns digest/latest/SHA, registry
  # allowlist, restore-test and healthcheck warnings into hard failures. A host
  # mid-migration can soften this back to false via hosts/<name>/policies.nix
  # (merged by lib/load-policies.nix).
  strict = true;
}
