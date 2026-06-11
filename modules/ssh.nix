{ config, lib, pkgs, env, ... }:

let
  e = import ../lib/env-lib.nix { inherit lib env; };
  sshPort = e.getInt "SSH_PORT" 22;
  passwordAuth = e.getBool "SSH_PASSWORD_AUTH" false;
in
{
  services.openssh = {
    enable = true;
    ports = [ sshPort ];
    settings = {
      PasswordAuthentication = passwordAuth;
      PermitRootLogin = "no";
      KbdInteractiveAuthentication = false;
      MaxAuthTries = 3;
      ClientAliveInterval = 300;
      ClientAliveCountMax = 2;
    };
  };

  services.fail2ban = {
    enable = true;
    maxretry = 5;
    bantime = "1h";
    bantime-increment = {
      enable = true;
      multipliers = "1 2 4 8 16 32 64";
      maxtime = "168h";
    };
    jails.sshd.settings = {
      enabled = true;
      port = toString sshPort;
      filter = "sshd";
      maxretry = 3;
    };
  };
}
