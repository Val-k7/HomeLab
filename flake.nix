{
  description = "Generic homelab NixOS configuration (multi-host fleet)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
    sops-nix.url = "github:Mic92/sops-nix";
    sops-nix.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = { self, nixpkgs, ... }@inputs:
    let
      system = "x86_64-linux";
      lib = nixpkgs.lib;
      loadEnv = import ./lib/load-env.nix { inherit lib; };
      pkgs = nixpkgs.legacyPackages.${system};

      # Every directory under hosts/ that carries a configuration.nix is a host.
      # Drop a new hosts/<name>/ and it becomes nixosConfigurations.<name> with
      # no change to this file. `homelab` stays the default single host.
      hostsDir = ./hosts;
      hostNames = builtins.attrNames (lib.filterAttrs
        (name: type: type == "directory"
          && builtins.pathExists (hostsDir + "/${name}/configuration.nix"))
        (builtins.readDir hostsDir));

      # builtins.getEnv returns "" unless evaluation runs with --impure
      # (deploy.sh passes it). In CI / pure eval both are empty, so resolution
      # falls through to the per-host or repo-root .env files below.
      explicitEnv = builtins.getEnv "HOMELAB_ENV";
      pwd = builtins.getEnv "PWD";

      # Per-host .env resolution (read at eval time, so commands pass --impure):
      #   1. $HOMELAB_ENV  — explicit override (used by deploy.sh, single target)
      #   2. hosts/<name>/.env — per-host file (multi-host layout)
      #   3. ./.env        — repo-root fallback (the v0.1 single-host layout)
      # The root fallback keeps an un-migrated host deploying while its .env is
      # still at the repo root.
      envFor = name:
        let
          perHost = hostsDir + "/${name}/.env";
          rootEnv = if pwd != "" then (pwd + "/.env") else ./.env;
        in
        if explicitEnv != "" then explicitEnv
        else if builtins.pathExists perHost then perHost
        else rootEnv;

      mkHost = name: nixpkgs.lib.nixosSystem {
        inherit system;
        specialArgs = {
          inherit inputs;
          env = loadEnv (envFor name);
          hostName = name;
        };
        modules = [ (hostsDir + "/${name}/configuration.nix") ];
      };
    in
    {
      nixosConfigurations = lib.genAttrs hostNames mkHost;

      checks.${system} = import ./tests { inherit pkgs lib; };
    };
}
