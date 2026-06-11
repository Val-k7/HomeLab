import { useSystem, useAppsState, usePolicies, useSecrets, useBackups, useChanges } from "../api/hooks";
import { Loading, pct, MetricCard, AlertBanner, SectionHead, StateBadge, Icon } from "../components";

function go(route: string) { window.location.hash = "#/" + route; }

export function Overview() {
  const sys = useSystem();
  const apps = useAppsState();
  const pol = usePolicies();
  const sec = useSecrets();
  const bk = useBackups();
  const ch = useChanges();

  const s: any = sys.data || {};
  const appList: any[] = (apps.data as any)?.apps || [];
  const drift = appList.filter((a) => a.drift).length;
  const downApps = appList.filter((a) => a.runtime && a.runtime.present && a.runtime.state !== "active").length;
  const policyErrors = ((pol.data as any)?.violations || []).filter((v: any) => v.severity === "error").length;
  const secMissing = (sec.data as any)?.missing_total ?? 0;
  const bkUncovered = (bk.data as any)?.uncovered ?? 0;
  const disk = parseFloat(s.disk);
  const changes: any[] = (ch.data as any)?.changes || [];
  const failedChanges = changes.filter((c) => {
    const checks = c.github?.statusCheckRollup;
    return Array.isArray(checks) && checks.some((x: any) => x.conclusion === "FAILURE" || x.state === "FAILURE");
  });

  // Aggregate signals into one global verdict for the hero banner.
  const issues: string[] = [];
  if (disk > 90) issues.push("disque critique");
  if (downApps) issues.push(`${downApps} app(s) down`);
  if (policyErrors) issues.push(`${policyErrors} violation(s) policy`);
  if (secMissing) issues.push(`${secMissing} secret(s) manquant(s)`);
  if (bkUncovered) issues.push(`${bkUncovered} backup(s) non couvert(s)`);
  if (drift) issues.push(`${drift} drift(s)`);
  if (failedChanges.length) issues.push(`${failedChanges.length} changement(s) en échec`);
  const critical = disk > 90 || downApps > 0 || policyErrors > 0;
  const tone = critical ? "bad" : issues.length ? "warn" : "ok";

  return (
    <div>
      <Loading q={sys} />

      <div className={`hero ${tone}`}>
        <span className="dot" />
        <div>
          <div className="h-title">
            {tone === "ok" ? "Système opérationnel" : tone === "warn" ? "Attention requise" : "Intervention critique"}
          </div>
          <div className="h-sub">
            {issues.length ? issues.join(" · ") : `${appList.length} apps saines · stockage et sauvegardes nominaux`}
          </div>
        </div>
        <div className="h-meta">
          gén. {s.generation ?? "–"}<br />
          {String(s.commit || "?").slice(0, 10)} · {s.deploy || "?"}
        </div>
      </div>

      {s.behind_main ? (
        <AlertBanner tone="warn" title="Configuration en retard sur main">
          Le commit déployé n'est pas à jour avec <span className="mono">main</span>. Déployez depuis l'écran Système.
        </AlertBanner>
      ) : null}

      <SectionHead title="Ressources système" />
      <div className="grid">
        <MetricCard label="CPU" icon="cpu" value={pct(s.cpu)} tone={disk > 90 ? undefined : undefined} gauge={parseFloat(s.cpu) || 0} />
        <MetricCard label="RAM" icon="ram" value={pct(s.mem)} gauge={parseFloat(s.mem) || 0} />
        <MetricCard label="Disque" icon="disk" value={pct(s.disk)} tone={disk > 90 ? "bad" : disk > 80 ? "warn" : "ok"} gauge={disk || 0} />
        <MetricCard label="Génération NixOS" icon="system" value={s.generation ?? "–"} sub={`load ${s.load1 ?? "–"}`} />
      </div>

      <SectionHead title="État de la plateforme" />
      <div className="grid">
        <MetricCard label="Apps" icon="apps" value={appList.length} sub={`${downApps} down`} tone={downApps ? "bad" : "neutral"} onClick={() => go("apps")} />
        <MetricCard label="Drift" icon="changes" value={drift} tone={drift ? "warn" : "ok"} onClick={() => go("apps")} />
        <MetricCard label="Violations policy" icon="security" value={policyErrors} tone={policyErrors ? "bad" : "ok"} onClick={() => go("security")} />
        <MetricCard label="Secrets manquants" icon="secrets" value={secMissing} tone={secMissing ? "bad" : "ok"} onClick={() => go("secrets")} />
        <MetricCard label="Backups non couverts" icon="backups" value={bkUncovered} tone={bkUncovered ? "bad" : "ok"} onClick={() => go("backups")} />
        <MetricCard label="Sync main" icon="refresh" value={s.behind_main ? "Behind" : "À jour"} tone={s.behind_main ? "warn" : "ok"} onClick={() => go("system")} />
      </div>

      <SectionHead title="Changements récents" count={changes.length} action={<a href="#/changes" className="muted">Tout voir →</a>} />
      {changes.length ? (
        <div className="table-wrap">
          <table>
            <thead><tr><th>Titre</th><th>Type</th><th>Statut</th><th>PR</th></tr></thead>
            <tbody>
              {changes.slice(0, 6).map((c) => (
                <tr key={c.pr_number ?? `${c.type}-${c.title}`}>
                  <td>{c.title}</td>
                  <td className="muted">{c.type}</td>
                  <td><StateBadge kind="action">{c.status}</StateBadge></td>
                  <td>{c.pr_url ? <a href={c.pr_url} target="_blank" rel="noreferrer">#{c.pr_number}</a> : <span className="faint">–</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="muted" style={{ display: "flex", alignItems: "center", gap: 8 }}><Icon name="check" /> Aucun changement en cours.</p>
      )}
    </div>
  );
}
