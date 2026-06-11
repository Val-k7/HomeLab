import { usePolicies, useAppsState } from "../api/hooks";
import { Loading, StateBadge, AlertBanner, SectionHead, MetricCard, Icon } from "../components";

// Render a policy value in a human-readable way without hiding the technical
// detail. Scalars become a chip; objects/arrays fall back to compact JSON.
function PolicyValue({ v }: { v: any }) {
  if (v === null || v === undefined) return <span className="faint">–</span>;
  if (typeof v === "boolean") return <StateBadge kind={v ? "runtime-ok" : "desired"}>{v ? "activé" : "désactivé"}</StateBadge>;
  if (typeof v === "string" || typeof v === "number") return <span className="chip">{String(v)}</span>;
  if (Array.isArray(v)) return <>{v.length ? v.map((x, i) => <span key={i} className="chip">{String(x)}</span>) : <span className="faint">vide</span>}</>;
  return <span className="mono" style={{ fontSize: 12 }}>{JSON.stringify(v)}</span>;
}

export function Security() {
  const pol = usePolicies();
  const apps = useAppsState();
  const violations: any[] = (pol.data as any)?.violations || [];
  const policies = (pol.data as any)?.policies || {};
  const appList: any[] = (apps.data as any)?.apps || [];

  const errors = violations.filter((v) => v.severity === "error");
  const warns = violations.filter((v) => v.severity !== "error");
  const policyEntries = Object.entries(policies);

  return (
    <div>
      <Loading q={pol} />

      {errors.length
        ? <AlertBanner tone="bad" title={`${errors.length} violation(s) bloquante(s)`}>Ces violations empêchent un déploiement conforme. Voir le détail ci-dessous.</AlertBanner>
        : <AlertBanner tone="ok" title="Aucune violation bloquante">Toutes les apps respectent les politiques de sécurité globales.</AlertBanner>}

      <div className="grid">
        <MetricCard label="Apps surveillées" icon="apps" value={appList.length} />
        <MetricCard label="Violations bloquantes" icon="security" value={errors.length} tone={errors.length ? "bad" : "ok"} />
        <MetricCard label="Avertissements" icon="warn" value={warns.length} tone={warns.length ? "warn" : "ok"} />
        <MetricCard label="Politiques globales" icon="shield" value={policyEntries.length} />
      </div>

      <SectionHead title="Permissions par application" count={appList.length} />
      <div className="table-wrap">
        <table>
          <thead><tr><th>App</th><th>Permissions demandées</th><th style={{ textAlign: "right" }}>Conformité</th></tr></thead>
          <tbody>
            {appList.map((a) => {
              const perms = a.desired?.permissions || [];
              const errs = (a.policy_status?.violations || []).filter((v: any) => v.severity === "error");
              return (
                <tr key={a.name}>
                  <td><b>{a.name}</b></td>
                  <td>{perms.length ? perms.map((p: string) => <span key={p} className="chip warn">{p}</span>) : <span className="faint">aucune</span>}</td>
                  <td style={{ textAlign: "right" }}>{errs.length ? <StateBadge kind="err">{errs.length} bloquant(s)</StateBadge> : <StateBadge kind="runtime-ok">conforme</StateBadge>}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      <SectionHead title="Violations" count={violations.length} />
      {violations.length ? (
        <div className="table-wrap">
          <table>
            <thead><tr><th>App</th><th>Code</th><th>Sévérité</th><th>Message</th><th>Correctif suggéré</th></tr></thead>
            <tbody>
              {violations.map((v) => (
                <tr key={`${v.app}-${v.code}`}>
                  <td><b>{v.app}</b></td><td className="mono">{v.code}</td>
                  <td><StateBadge kind={v.severity === "error" ? "err" : "risk"}>{v.severity}</StateBadge></td>
                  <td className="muted">{v.message}</td>
                  <td className="muted">{v.hint || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : <p className="muted" style={{ display: "flex", gap: 8, alignItems: "center" }}><Icon name="check" /> Aucune violation détectée.</p>}

      <SectionHead title="Politiques globales" count={policyEntries.length} sub="config/policies.nix — modifiable via une PR" action={<button className="btn secondary sm" onClick={() => { location.hash = "#/settings"; }}><Icon name="settings" /> Éditer les politiques</button>} />
      <div className="table-wrap">
        <table>
          <thead><tr><th>Politique</th><th>Valeur</th></tr></thead>
          <tbody>
            {policyEntries.map(([k, v]) => (
              <tr key={k}><td><b>{k}</b></td><td><PolicyValue v={v} /></td></tr>
            ))}
            {!policyEntries.length && <tr><td colSpan={2} className="muted">aucune politique déclarée</td></tr>}
          </tbody>
        </table>
      </div>

      <details className="raw">
        <summary><Icon name="terminal" /> Configuration brute (JSON)</summary>
        <pre>{JSON.stringify(policies, null, 2)}</pre>
      </details>
    </div>
  );
}
