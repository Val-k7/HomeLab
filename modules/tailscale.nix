{ config, lib, pkgs, env, ... }:

let
  e = import ../lib/env-lib.nix { inherit lib env; };
  sshPort = e.getInt "SSH_PORT" 22;
in
{
  services.tailscale = {
    enable = true;
    authKeyFile =
      if (config.sops.secrets ? tailscale_authkey)
      then config.sops.secrets.tailscale_authkey.path
      else (e.get "TAILSCALE_AUTHKEY_FILE" "/etc/tailscale/authkey");
    extraUpFlags = [ "--ssh" ];
  };

  # Upstream services.tailscale already orders tailscaled after
  # network-pre.target; it does not wait for network-online, so on slow links
  # the auth/up step can race DHCP/DNS. Only the missing ordering is added here
  # (lists merge with upstream's).
  systemd.services.tailscaled = {
    wants = [ "network-online.target" ];
    after = [ "network-online.target" ];
  };

  networking.firewall = {
    allowedUDPPorts = [ config.services.tailscale.port ];
    interfaces.tailscale0.allowedTCPPorts = [ sshPort ];
  };

  environment.systemPackages = with pkgs; [
    tailscale
  ];
}
