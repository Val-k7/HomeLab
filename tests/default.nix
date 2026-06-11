# Flake checks for the HomeLab platform. Each entry forces eval of an
# eval-time test (which throws on failure) by wiring its result into a
# trivial derivation. `nix flake check` runs them all.
{ pkgs, lib }:

let
  mk = name: result:
    pkgs.runCommand "test-${name}" { inherit result; } ''
      printf '%s\n' "$result" > "$out"
    '';
in
{
  app-model = mk "app-model" (import ./app-model.nix { inherit lib; });
  storage = mk "storage" (import ./storage.nix { inherit lib; });
  multi-host = mk "multi-host" (import ./multi-host.nix { inherit lib; });
  catalog = mk "catalog" (import ./catalog.nix { inherit lib; });

  # Heavy check (NixOS VM test, needs build + KVM). Evaluated by
  # `nix flake check --no-build` but only built/run by the dedicated
  # restore-e2e CI job and the release gate.
  restore-e2e = import ./restore-e2e.nix { inherit pkgs lib; };
}
