import { Component, ErrorInfo, ReactNode, useEffect, useState } from "react";
import { useMe, useSystem, usePlatform, useDrift } from "./api/hooks";
import { Icon } from "./components";
import { Overview } from "./screens/Overview";
import { Apps } from "./screens/Apps";
import { Library } from "./screens/Library";
import { Changes } from "./screens/Changes";
import { Storage } from "./screens/Storage";
import { Secrets } from "./screens/Secrets";
import { Backups } from "./screens/Backups";
import { Security } from "./screens/Security";
import { Monitoring } from "./screens/Monitoring";
import { System } from "./screens/System";
import { Settings } from "./screens/Settings";
import { Audit } from "./screens/Audit";

const ROLE_RANK: Record<string, number> = { viewer: 0, operator: 1, maintainer: 2, admin: 3 };

// minRole gates only what the screen exists FOR (mutating sensitive config).
// Every action is also enforced server-side; this is UX, not the security
// boundary. Unknown role falls back to viewer (most restrictive).
type ScreenDef = { label: string; el: () => JSX.Element; icon: string; minRole?: string };
const SCREENS: Record<string, ScreenDef> = {
  overview: { label: "Vue d'ensemble", el: Overview, icon: "overview" },
  apps: { label: "Apps", el: Apps, icon: "apps" },
  library: { label: "Bibliothèque", el: Library, icon: "library" },
  changes: { label: "Changements", el: Changes, icon: "changes" },
  storage: { label: "Stockage", el: Storage, icon: "storage" },
  secrets: { label: "Secrets", el: Secrets, icon: "secrets", minRole: "maintainer" },
  backups: { label: "Sauvegardes", el: Backups, icon: "backups" },
  security: { label: "Sécurité", el: Security, icon: "security" },
  monitoring: { label: "Supervision", el: Monitoring, icon: "monitoring" },
  audit: { label: "Audit", el: Audit, icon: "history", minRole: "operator" },
  system: { label: "Système", el: System, icon: "system" },
  settings: { label: "Réglages", el: Settings, icon: "settings", minRole: "admin" },
};

// Visual grouping of the nav — purely presentational, gating still per-screen.
const NAV_GROUPS: { label: string; keys: string[] }[] = [
  { label: "Pilotage", keys: ["overview"] },
  { label: "Applications", keys: ["apps", "library", "changes"] },
  { label: "Infrastructure", keys: ["storage", "secrets", "backups"] },
  { label: "Opérations", keys: ["security", "monitoring", "audit", "system"] },
  { label: "Plateforme", keys: ["settings"] },
];

function allowed(key: string, role: string): boolean {
  const min = SCREENS[key]?.minRole;
  if (!min) return true;
  return (ROLE_RANK[role] ?? 0) >= (ROLE_RANK[min] ?? 99);
}

function currentRoute() {
  const h = window.location.hash.replace(/^#\/?/, "");
  return SCREENS[h] ? h : "overview";
}

// Catches render-time errors in any screen so one broken screen shows a message
// instead of blanking the whole app. Reset key = route, so navigating away clears.
class ErrorBoundary extends Component<{ resetKey: string; children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  componentDidUpdate(prev: { resetKey: string }) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null });
  }
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("screen crashed:", error, info.componentStack);
  }
  render() {
    if (this.state.error) {
      return (
        <div className="alert bad" style={{ margin: 0 }}>
          <Icon name="xcircle" />
          <div className="a-body">
            <div className="a-title">Cet écran a planté.</div>
            <div className="a-text">{this.state.error.message} — change d'écran ou recharge la page.</div>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}

const Logo = () => (
  <svg className="logo" viewBox="0 0 32 32" aria-hidden>
    <polygon points="16,3 27,9.5 27,22.5 16,29 5,22.5 5,9.5" fill="#5468ff" />
    <g fill="#fff"><rect x="11" y="9" width="3" height="14" rx="1" /><rect x="18" y="9" width="3" height="14" rx="1" /><rect x="11" y="14.5" width="10" height="3" rx="1" /></g>
    <circle cx="25" cy="25" r="4" fill="#2dd4bf" />
  </svg>
);

// Compact global-health pill in the topbar. Green unless disk is high or the
// deployed commit is behind main — enough to answer "should I look?" at a glance.
function GlobalStatus() {
  const sys = useSystem();
  const s: any = sys.data || {};
  const disk = parseFloat(s.disk);
  const tone = disk > 90 ? "bad" : s.behind_main || disk > 80 ? "warn" : "ok";
  const text = sys.isLoading ? "…" : tone === "bad" ? "Disque critique" : s.behind_main ? "Behind main" : tone === "warn" ? "Attention" : "Opérationnel";
  return (
    <span className={`badge ${tone === "ok" ? "runtime-ok" : tone === "warn" ? "risk" : "err"}`} title="État global du système">
      {text}
    </span>
  );
}

// Host identity from the platform manifest — the control-api is single-host, so
// this labels which machine the cockpit is driving (not a host selector).
function HostChip() {
  const plat = usePlatform();
  const host = (plat.data as any)?.host?.hostname;
  if (!host) return null;
  return (
    <span className="badge info nodot" title="Hôte piloté par ce control plane" style={{ fontFamily: "var(--mono)" }}>
      <Icon name="server" /> {host}
    </span>
  );
}

// Drift vs origin/main (serveur: cache 15min sur ls-remote). Masqué quand le
// déploiement est à jour ; orange si derrière main ; gris si l'état est inconnu
// (ls-remote en échec depuis trop longtemps).
function DriftChip() {
  const drift = useDrift();
  const d: any = drift.data;
  if (!d) return null;
  if (d.behind) {
    return (
      <span className="badge risk" title={`Déployé: ${(d.deployed_commit || "?").slice(0, 8)} · main: ${(d.main_commit || "?").slice(0, 8)}`}>
        Drift: derrière main
      </span>
    );
  }
  if (d.stale) {
    return (
      <span className="badge desired" title="Impossible de vérifier origin/main récemment (ls-remote en échec)">
        Drift: inconnu
      </span>
    );
  }
  return null;
}

export function App() {
  const [route, setRoute] = useState(currentRoute());
  const me = useMe();
  const role = (me.data as any)?.role ?? "viewer";
  const email = (me.data as any)?.email as string | undefined;

  useEffect(() => {
    const onHash = () => setRoute(currentRoute());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  // If the active route is not permitted for this role, fall back to overview.
  const activeRoute = allowed(route, role) ? route : "overview";
  const Screen = SCREENS[activeRoute].el;
  const initials = (email || "?").slice(0, 2).toUpperCase();

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="brand">
          <Logo />
          <div>
            <h1>HomeLab</h1>
            <div className="ver">control plane</div>
          </div>
        </div>
        <nav className="nav">
          {NAV_GROUPS.map((g) => {
            const keys = g.keys.filter((k) => allowed(k, role));
            if (!keys.length) return null;
            return (
              <div className="nav-group" key={g.label}>
                <div className="label">{g.label}</div>
                {keys.map((key) => (
                  <button key={key} className={key === activeRoute ? "active" : ""} onClick={() => (window.location.hash = "#/" + key)}>
                    <Icon name={SCREENS[key].icon} />
                    {SCREENS[key].label}
                  </button>
                ))}
              </div>
            );
          })}
        </nav>
      </aside>
      <main className="main">
        <div className="topbar">
          <div className="crumb">
            <span className="eyebrow">HomeLab Control Plane</span>
            <h2>{SCREENS[activeRoute].label}</h2>
          </div>
          <div className="right">
            <HostChip />
            <DriftChip />
            <GlobalStatus />
            <span className="id">
              <span className="avatar">{initials}</span>
              {me.data ? <span>{email}</span> : <span>…</span>}
              <span className="role">{role}</span>
            </span>
          </div>
        </div>
        <div className="content">
          <ErrorBoundary resetKey={activeRoute}>
            <Screen />
          </ErrorBoundary>
        </div>
      </main>
    </div>
  );
}
