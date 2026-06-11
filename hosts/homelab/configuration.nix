{ config, lib, pkgs, inputs, env, ... }:

let
  e = import ../../lib/env-lib.nix { inherit lib env; };
  username = e.get "USERNAME" "admin";
  # Same checkout convention control-api.nix uses (HOMELAB_DIR).
  homelabDir = "/home/${username}/homelab";
  # SECURITY: the file SSH_AUTHORIZED_KEYS_FILE points at must be root-owned
  # with mode 0600 — its contents become authorized SSH keys for the admin
  # user. Pure Nix cannot check ownership/permissions at eval time, so this
  # is enforced by convention only.
  keysFile = e.get "SSH_AUTHORIZED_KEYS_FILE" "";
  keysFromFile =
    if keysFile != "" && builtins.pathExists keysFile then
      lib.filter (l: l != "" && !(lib.hasPrefix "#" l))
        (map lib.trim (lib.splitString "\n" (builtins.readFile keysFile)))
    else [ ];
  keysInline = e.getList "SSH_AUTHORIZED_KEYS" [ ];
  authorizedKeys = lib.unique (keysFromFile ++ keysInline);
in
{
  imports = [
    ./hardware-configuration.nix
    ../../modules/networking.nix
    ../../modules/ssh.nix
    ../../modules/docker.nix
    ../../modules/tailscale.nix
    ../../modules/platform.nix
    ../../modules/apps.nix
    ../../modules/control-api.nix
    ../../modules/auth.nix
    ../../modules/backup.nix
    ../../modules/alerting.nix
    ../../modules/observability.nix
    ../../modules/secrets.nix
    inputs.sops-nix.nixosModules.sops
  ];

  boot.loader.systemd-boot.enable = true;
  boot.loader.efi.canTouchEfiVariables = true;

  time.timeZone = e.get "TIMEZONE" "UTC";
  i18n.defaultLocale = e.get "LOCALE" "en_US.UTF-8";

  users.users.${username} = {
    isNormalUser = true;
    description = e.get "USER_DESCRIPTION" "Homelab admin";
    extraGroups = [ "wheel" "docker" ];
    openssh.authorizedKeys.keys = authorizedKeys;
  };

  security.sudo.wheelNeedsPassword = e.getBool "SUDO_NEEDS_PASSWORD" true;

  environment.systemPackages = with pkgs; [
    git
    curl
    wget
    htop
    vim
    unzip
  ];

  nix.settings = {
    experimental-features = [ "nix-command" "flakes" ];
    auto-optimise-store = true;
  };

  nix.gc = {
    automatic = true;
    dates = "weekly";
    options = "--delete-older-than 30d";
  };

  # Disk hygiene: cap the journal so logs cannot fill the root filesystem.
  services.journald.extraConfig = "SystemMaxUse=1G\nMaxRetentionSec=1month";

  # Monthly secrets-rotation reminder: fails (visible in systemctl/journal) when
  # any sops secret's last commit is older than 90d. Runs against the operator's
  # checkout, the same HOMELAB_DIR convention control-api uses.
  systemd.services.secrets-age-check = {
    description = "check sops secrets rotation age";
    onFailure = [ "alert@%n.service" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "${homelabDir}/bin/rotate-secrets.sh --check-age";
      WorkingDirectory = homelabDir;
    };
    # The script needs git (history age), find, date; bash for its shebang.
    path = [ pkgs.bash pkgs.git pkgs.coreutils pkgs.findutils ];
    # Root inspecting the admin user's checkout: tell git the directory is safe.
    environment = {
      GIT_CONFIG_COUNT = "1";
      GIT_CONFIG_KEY_0 = "safe.directory";
      GIT_CONFIG_VALUE_0 = homelabDir;
    };
  };
  systemd.timers.secrets-age-check = {
    description = "schedule monthly secrets age check";
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnCalendar = "*-*-01 06:00:00";
      Persistent = true;
      RandomizedDelaySec = "30m";
    };
  };

  system.stateVersion = "25.05";
}
