# Persistent Data Model

This document clarifies which data survives redeployments. The repository
rebuilds the system, but application data lives outside Git.

> **Type:** explanation · **Audience:** operator · **Last reviewed:** 2026-06-11

## Current Detected State

| Area | Example | Owner | Backup |
| --- | --- | --- | --- |
| Homelab state | `/var/lib/homelab/*.jsonl` | `control-api` | yes |
| Process/dockerfile apps | `/var/lib/app-<name>` | `modules/apps.nix` | per app |
| Image apps | Docker container `app-<name>` | Docker/systemd | volumes to be declared |
| Compose apps | `dir` path plus compose volumes | Docker Compose | per app |

## Target Fields Per App

The target model for each `apps/<name>.nix` should include:

```nix
{
  runner = "image";
  port = 8080;
  persistentVolumes = [
    {
      name = "data";
      path = "/var/lib/app-demo/data";
      backup = true;
      restore = "restore restic snapshot then restart app-demo.service";
    }
  ];
  dependencies = [ "docker.service" ];
  criticality = "medium";
}
```

## Rules

- no implicit critical volume;
- no secret in version-controlled `env`;
- `envFile` must point to a file managed outside Git;
- a critical app without a restoration procedure is incomplete;
- any exposed port must stay restricted to the tailnet unless explicitly decided
  otherwise.

## Next Evolution

Add validation in `control-api` and in the CI checks to reject a critical app
that has no persistence declaration or backup policy.
