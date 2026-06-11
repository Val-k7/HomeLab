# Authentication front (HomeLab Platform V2 — standalone UI).
#
# oauth2-proxy (GitHub OIDC) sits in front of control-api and is the only thing
# that injects identity headers (X-Forwarded-Email/User). control-api listens
# on loopback, so it only ever receives oauth2-proxy traffic — client-supplied
# identity headers cannot reach it. `tailscale serve` exposes oauth2-proxy on
# the tailnet over HTTPS. Nothing is published on a raw public IP.
#
# ACCESS IS RESTRICTED BY DESIGN: this is a private homelab. The build FAILS
# unless at least one GitHub restriction is set (org and/or explicit user
# allowlist), so authentication can never be left open to any GitHub account.
# Set in .env:
#   OAUTH2_GITHUB_ORG=my-org           # only members of this org, and/or
#   OAUTH2_GITHUB_USERS=alice,bob       # only these GitHub logins
#
# Provision before deploy (via SOPS, see modules/secrets.nix):
#   /run/secrets/oauth2_proxy_env  containing
#     OAUTH2_PROXY_CLIENT_ID=...
#     OAUTH2_PROXY_CLIENT_SECRET=...
#     OAUTH2_PROXY_COOKIE_SECRET=...   (16/24/32-CHAR string, e.g. `openssl rand -base64 24`)
{ config, lib, pkgs, env, ... }:

let
  e = import ../lib/env-lib.nix { inherit lib env; };
  apiPort = e.getInt "CONTROL_API_PORT" 9092;
  proxyPort = 4180;

  githubOrg = e.get "OAUTH2_GITHUB_ORG" "";
  githubUsers = lib.filter (s: s != "")
    (map lib.trim (lib.splitString "," (e.get "OAUTH2_GITHUB_USERS" "")));

  restricted = githubOrg != "" || githubUsers != [ ];
in
{
  # Fail-closed: refuse to build an open auth front. A homelab must not be
  # reachable by every GitHub account.
  assertions = [
    {
      assertion = restricted;
      message = "auth.nix: set OAUTH2_GITHUB_ORG and/or OAUTH2_GITHUB_USERS in .env — refusing to allow any GitHub account to authenticate.";
    }
  ];

  services.oauth2-proxy = {
    enable = true;
    provider = "github";
    httpAddress = "127.0.0.1:${toString proxyPort}";
    reverseProxy = true;
    setXauthrequest = true;
    upstream = [ "http://127.0.0.1:${toString apiPort}" ];
    email.domains = [ "*" ];
    keyFile = "/run/secrets/oauth2_proxy_env";
    extraConfig = {
      "pass-user-headers" = "true";
      "set-xauthrequest" = "true";
      "skip-provider-button" = "true";
      "cookie-secure" = "true";
      "cookie-samesite" = "lax";
      "cookie-expire" = "168h";
      "cookie-refresh" = "1h";
    }
    // lib.optionalAttrs (githubOrg != "") { "github-org" = githubOrg; }
    // lib.optionalAttrs (githubUsers != [ ]) { "github-user" = githubUsers; };
  };

  # Expose oauth2-proxy on the tailnet over HTTPS (MagicDNS + auto certs).
  systemd.services.tailscale-serve = {
    description = "expose control plane via tailscale serve";
    after = [ "tailscaled.service" "oauth2-proxy.service" ];
    wants = [ "tailscaled.service" ];
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = "${pkgs.tailscale}/bin/tailscale serve --bg --https=443 http://127.0.0.1:${toString proxyPort}";
      ExecStop = "${pkgs.tailscale}/bin/tailscale serve --https=443 off";
    };
  };
}
