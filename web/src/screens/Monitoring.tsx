import { useState } from "react";
import { useObservability } from "../api/hooks";
import { Loading, StateBadge, Dialog, pct, AlertBanner, SectionHead, MetricCard, Icon } from "../components";
import { apiGet } from "../api/client";

// Internal observability — three tiers, all sourced from control-api (no external
// metrics system): global roll-up, the control-plane's own infrastructure, and
// per-application runtime.

function fmtBytes(n: number): string {
  if (!n || n < 0) return "–";
  const u = ["o", "Ko", "Mo", "Go", "To"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${u[i]}`;
}

function fmtUptime(sec: number): string {
  if (!sec || sec < 0) return "–";
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d}j ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export function Monitoring() {
  const obs = useObservability();
  const [logs, setLogs] = useState<{ app: string; text: string } | null>(null);
  const d: any = obs.data || {};
  const g: any = d.global || {};
  const infra: any[] = d.infra || [];
  const apps: any[] = d.apps || [];

  const showLogs = async (app: string) => {
    try {
      const r: any = await apiGet(`/v1/logs?app=${app}`);
      setLogs({ app, text: r.logs || "(vide)" });
    } catch (e: any) {
      setLogs({ app, text: e.message });
    }
  };

  // Infra components whose journal the API exposes (server-side allowlist).
  const INFRA_LOG_UNITS = new Set(["control-api", "oauth2-proxy", "docker", "tailscaled"]);
  const showInfraLogs = async (unit: string) => {
    try {
      const r: any = await apiGet(`/v1/logs/infra?unit=${unit}`);
      setLogs({ app: unit, text: r.logs || "(vide)" });
    } catch (e: any) {
      setLogs({ app: unit, text: e.message });
    }
  };

  const infraDown = infra.filter((c) => !c.healthy);
  const appsDown = apps.filter((a) => a.has_healthcheck && !a.healthy);

  return (
    <div>
      <AlertBanner tone="info" title="Observabilité interne">
        Métriques collectées directement par control-api (hôte, infrastructure du projet et applications). Aucun système externe.
      </AlertBanner>
      <Loading q={obs} />

      {infraDown.length ? <AlertBanner tone="bad" title={`${infraDown.length} composant(s) d'infra en échec`}>{infraDown.map((c) => c.label).join(" · ")}</AlertBanner> : null}
      {appsDown.length ? <AlertBanner tone="warn" title={`${appsDown.length} app(s) en échec de healthcheck`}>{appsDown.map((a) => a.app).join(" · ")}</AlertBanner> : null}

      {/* ---- Tier 1: Global ---- */}
      <SectionHead title="Global" sub="vue d'ensemble de l'hôte et des roll-ups" />
      <div className="grid">
        <MetricCard label="Apps saines" icon="heart" value={`${g.apps_healthy ?? 0}/${g.apps_monitored ?? 0}`} sub={`${g.apps_total ?? 0} app(s) au total`} tone={g.apps_down ? "warn" : "ok"} />
        <MetricCard label="Infra OK" icon="server" value={`${g.infra_ok ?? 0}/${(g.infra_ok ?? 0) + (g.infra_down ?? 0)}`} tone={g.infra_down ? "bad" : "ok"} />
        <MetricCard label="CPU" icon="cpu" value={pct(g.cpu)} gauge={parseFloat(g.cpu) || 0} />
        <MetricCard label="RAM" icon="ram" value={pct(g.mem)} gauge={parseFloat(g.mem) || 0} />
        <MetricCard label="Disque" icon="disk" value={pct(g.disk)} tone={parseFloat(g.disk) > 90 ? "bad" : "ok"} gauge={parseFloat(g.disk) || 0} />
        <MetricCard label="Génération" icon="system" value={g.generation ?? "–"} sub={g.uptime_sec ? `up ${fmtUptime(g.uptime_sec)}` : undefined} />
      </div>

      {/* ---- Tier 2: Project infrastructure ---- */}
      <SectionHead title="Infrastructure du projet" count={infra.length} sub="control-api · web · NixOS · runtime — surveillés à part des apps" />
      <div className="table-wrap">
        <table>
          <thead><tr><th>Composant</th><th>Type</th><th>État</th><th>Détail</th><th style={{ textAlign: "right" }}></th></tr></thead>
          <tbody>
            {infra.map((c) => (
              <tr key={c.name}>
                <td><b>{c.label}</b><div className="mono">{c.name}</div></td>
                <td className="muted">{c.kind === "control-plane" ? "control-plane" : "plateforme"}</td>
                <td><StateBadge kind={c.healthy ? "runtime-ok" : "runtime-bad"}>{c.state}</StateBadge></td>
                <td className="muted">{c.detail || ""}</td>
                <td style={{ textAlign: "right" }}>
                  {INFRA_LOG_UNITS.has(c.name) ? (
                    <button className="btn secondary sm" onClick={() => showInfraLogs(c.name)}><Icon name="terminal" /> Logs</button>
                  ) : null}
                </td>
              </tr>
            ))}
            {!infra.length && <tr><td colSpan={5} className="muted">aucun composant remonté</td></tr>}
          </tbody>
        </table>
      </div>

      {/* ---- Tier 3: Applications ---- */}
      <SectionHead title="Applications" count={apps.length} sub="runtime par app : état, santé, ressources" />
      <div className="table-wrap">
        <table>
          <thead><tr>
            <th>App</th><th>État</th><th>Santé</th>
            <th style={{ textAlign: "right" }}>CPU</th><th style={{ textAlign: "right" }}>RAM</th>
            <th style={{ textAlign: "right" }}>Redém.</th><th style={{ textAlign: "right" }}>Uptime</th>
            <th style={{ textAlign: "right" }}>Logs</th>
          </tr></thead>
          <tbody>
            {apps.map((a) => (
              <tr key={a.app}>
                <td><b>{a.app}</b></td>
                <td><StateBadge kind={a.state === "active" ? "runtime-ok" : "runtime-bad"}>{a.sub || a.state}</StateBadge></td>
                <td>{a.has_healthcheck ? <StateBadge kind={a.healthy ? "runtime-ok" : "runtime-bad"}>{a.healthy ? "healthy" : "down"}</StateBadge> : <span className="faint">n/a</span>}</td>
                <td style={{ textAlign: "right" }} className="mono">{a.state === "active" ? `${(a.cpu_percent || 0).toFixed(1)}%` : "–"}</td>
                <td style={{ textAlign: "right" }} className="mono">{a.state === "active" ? fmtBytes(a.mem_bytes) : "–"}</td>
                <td style={{ textAlign: "right" }} className="mono">{a.restarts || 0}</td>
                <td style={{ textAlign: "right" }} className="mono">{fmtUptime(a.uptime_sec)}</td>
                <td style={{ textAlign: "right" }}><button className="btn secondary sm" onClick={() => showLogs(a.app)}><Icon name="terminal" /> Logs</button></td>
              </tr>
            ))}
            {!apps.length && <tr><td colSpan={8} className="muted">aucune app</td></tr>}
          </tbody>
        </table>
      </div>

      {logs && (
        <Dialog title={`Logs · ${logs.app}`} onClose={() => setLogs(null)}>
          <div className="logbox"><div className="log-head"><Icon name="terminal" /> journalctl · {logs.app}</div><pre>{logs.text}</pre></div>
        </Dialog>
      )}
    </div>
  );
}
