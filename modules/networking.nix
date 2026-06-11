{ config, lib, pkgs, env, hostName ? "homelab", ... }:

let
  e = import ../lib/env-lib.nix { inherit lib env; };
  iface = e.get "INTERFACE" "";
  useDhcp = e.getBool "USE_DHCP" true;
  staticIp = e.get "STATIC_IP" "";
  prefix = e.getInt "PREFIX_LENGTH" 24;
  gateway = e.get "GATEWAY" "";
  sshPort = e.getInt "SSH_PORT" 22;
  sshOpenFirewall = e.getBool "SSH_OPEN_FIREWALL" false;
in
{
  options.homelab.network.isolateContainers = lib.mkOption {
    type = lib.types.bool;
    default = true;
    description = ''
      Default-deny traffic from docker bridge interfaces (docker0 and the
      br-* bridges compose creates) to the host itself. Containers cannot
      reach 127.0.0.1-bound host services anyway (loopback is non-routable
      and route_localnet is off), so the real exposure is host services
      bound to 0.0.0.0 or to the bridge IP. This blocks those too, allowing
      only established/related return traffic and DNS to the host.
      Container-to-internet and container-to-container traffic (FORWARD
      chain, managed by docker) is unaffected.
    '';
  };

  config = {
    assertions = [
      {
        assertion = useDhcp || (staticIp != "" && gateway != "" && iface != "");
        message = "Static networking needs STATIC_IP, GATEWAY and INTERFACE set in .env, or set USE_DHCP=true.";
      }
      {
        assertion = sshPort >= 1 && sshPort <= 65535;
        message = "SSH_PORT must be between 1 and 65535";
      }
    ];

    networking = lib.mkMerge [
      {
        # Default to the flake host name (the hosts/<name>/ directory); .env
        # HOSTNAME still wins if set, for hosts not yet migrated to per-host dirs.
        hostName = e.get "HOSTNAME" hostName;
        enableIPv6 = e.getBool "ENABLE_IPV6" false;
        nameservers = e.getList "NAMESERVERS" [ "1.1.1.1" "9.9.9.9" ];
        firewall = {
          enable = true;
          allowedTCPPorts = lib.optional sshOpenFirewall sshPort;
        };
      }
      (lib.mkIf config.homelab.network.isolateContainers {
        # Container -> host default-deny (iptables backend; docker manages its
        # own chains there, so nftables is not in use on these hosts). A
        # dedicated chain is hooked at the top of INPUT for docker0 and the
        # per-compose-project br-* bridges; established/related replies (e.g.
        # to ports docker publishes) and DNS to the host stay allowed. The -C
        # guard keeps the hook idempotent across firewall reloads.
        firewall.extraCommands = ''
          iptables -N homelab-ctr-isolation 2>/dev/null || true
          iptables -F homelab-ctr-isolation
          iptables -A homelab-ctr-isolation -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
          iptables -A homelab-ctr-isolation -p udp --dport 53 -j RETURN
          iptables -A homelab-ctr-isolation -p tcp --dport 53 -j RETURN
          iptables -A homelab-ctr-isolation -j DROP
          iptables -C INPUT -i docker0 -j homelab-ctr-isolation 2>/dev/null \
            || iptables -I INPUT 1 -i docker0 -j homelab-ctr-isolation
          iptables -C INPUT -i br-+ -j homelab-ctr-isolation 2>/dev/null \
            || iptables -I INPUT 1 -i br-+ -j homelab-ctr-isolation
        '';
        firewall.extraStopCommands = ''
          iptables -D INPUT -i docker0 -j homelab-ctr-isolation 2>/dev/null || true
          iptables -D INPUT -i br-+ -j homelab-ctr-isolation 2>/dev/null || true
          iptables -F homelab-ctr-isolation 2>/dev/null || true
          iptables -X homelab-ctr-isolation 2>/dev/null || true
        '';
      })
      (lib.mkIf useDhcp {
        useDHCP = true;
      })
      (lib.mkIf (!useDhcp) {
        useDHCP = false;
        defaultGateway = { address = gateway; interface = iface; };
        interfaces.${iface} = {
          useDHCP = false;
          ipv4.addresses = [
            {
              address = staticIp;
              prefixLength = prefix;
            }
          ];
        };
      })
    ];
  };
}
