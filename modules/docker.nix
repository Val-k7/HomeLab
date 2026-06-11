{ config, pkgs, ... }:

{
  virtualisation.docker = {
    enable = true;
    package = pkgs.docker_29;
    enableOnBoot = true;
    autoPrune = {
      enable = true;
      dates = "weekly";
      # Only prune images/containers/networks older than a week; never volumes
      # (app data may live there — `docker system prune` without --volumes
      # leaves them alone).
      flags = [ "--all" "--filter" "until=168h" ];
    };
    daemon.settings = {
      log-driver = "json-file";
      log-opts = {
        max-size = "10m";
        max-file = "3";
      };
    };
  };

  environment.systemPackages = with pkgs; [
    docker-compose
  ];
}
