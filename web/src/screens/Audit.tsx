import { useMemo, useState } from "react";
import { useAudit } from "../api/hooks";
import { Loading, StateBadge, SectionHead, MetricCard, EmptyState, Icon, useConfirm, useMsg } from "../components";
import { apiPost } from "../api/client";

// Download the currently filtered events as a file. Client-side: the data is
// already loaded; the export honors both the server filter and the text filter.
function download(name: string, mime: string, text: string) {
  const a = document.createElement("a");
  a.href = URL.createObjectURL(new Blob([text], { type: mime }));
  a.download = name;
  a.click();
  URL.revokeObjectURL(a.href);
}
const CSV_COLS = ["time", "actor", "op", "kind", "risk", "target", "result", "status", "commit", "job_id", "error", "message"];
function toCSV(events: any[]): string {
  const esc = (v: any) => { const s = v == null ? "" : String(v); return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s; };
  return [CSV_COLS.join(","), ...events.map((e) => CSV_COLS.map((c) => esc(e[c])).join(","))].join("\n");
}

// result → badge tone. failed/blocked are the ones an operator hunts for.
function tone(result: string): "runtime-ok" | "runtime-bad" | "risk" | "action" | "err" {
  if (result === "failed" || result === "blocked") return "err";
  if (result === "armed") return "risk";
  if (result === "started" || result === "ok" || result === "success") return "runtime-ok";
  return "action";
}

const RESULTS = ["", "failed", "blocked", "started", "armed", "info"];

export function Audit() {
  const [result, setResult] = useState("");
  const [includeUi, setIncludeUi] = useState(false);
  const audit = useAudit({ limit: 300, result, includeUi });
  const [filter, setFilter] = useState("");
  const events: any[] = (audit.data as any)?.events || [];

  // Backend already returns newest-first (reverseAudit in readAuditEvents);
  // re-reversing here displayed oldest-first.
  const ordered = events;
  const shown = useMemo(
    () => filter
      ? ordered.filter((e) => JSON.stringify(e).toLowerCase().includes(filter.toLowerCase()))
      : ordered,
    [ordered, filter],
  );

  const failed = events.filter((e) => e.result === "failed" || e.result === "blocked").length;
  const risky = events.filter((e) => e.risk === "risky").length;

  const { confirm, node: confirmNode } = useConfirm();
  const { setMsg, node: msgNode } = useMsg();
  const prune = async () => {
    if (!(await confirm("Purger le journal d'audit (et l'historique des déploiements) ? Irréversible.", { danger: true, confirmLabel: "Purger" })).ok) return;
    try {
      await apiPost("/v1/audit/prune", { targets: ["audit", "deployments"] });
      setMsg({ text: "Journal purgé", ok: true });
      (audit as any).refetch?.();
    } catch (e: any) {
      setMsg({ text: e.message, ok: false });
    }
  };

  return (
    <div>
      {confirmNode}
      {msgNode}
      <Loading q={audit} />

      <SectionHead title="Journal d'audit" sub="toutes les actions auditées du control plane" />
      <div className="grid">
        <MetricCard label="Événements" icon="history" value={events.length} />
        <MetricCard label="Échecs / bloqués" icon="xcircle" value={failed} tone={failed ? "bad" : "ok"} />
        <MetricCard label="Actions à risque" icon="warn" value={risky} tone={risky ? "warn" : "ok"} />
      </div>

      <div className="section-head">
        <h3 style={{ margin: 0 }}>Événements <span className="count">· {shown.length}</span></h3>
        <div className="row">
          <select value={result} onChange={(e) => setResult(e.target.value)} title="Filtre résultat (serveur)">
            {RESULTS.map((r) => <option key={r} value={r}>{r || "tous résultats"}</option>)}
          </select>
          <label className="row" style={{ gap: 6, fontSize: 12, color: "var(--muted)" }}>
            <input type="checkbox" checked={includeUi} onChange={(e) => setIncludeUi(e.target.checked)} /> actions UI
          </label>
          <input placeholder="Filtrer (acteur, op, cible…)" value={filter} onChange={(e) => setFilter(e.target.value)} style={{ minWidth: 200 }} />
          <button className="btn secondary sm" disabled={!shown.length}
            onClick={() => download(`audit-${new Date().toISOString().slice(0, 10)}.csv`, "text/csv", toCSV(shown))}>
            <Icon name="download" /> CSV
          </button>
          <button className="btn secondary sm" disabled={!shown.length}
            onClick={() => download(`audit-${new Date().toISOString().slice(0, 10)}.json`, "application/json", JSON.stringify(shown, null, 2))}>
            <Icon name="download" /> JSON
          </button>
          <button className="btn danger sm" onClick={prune}><Icon name="xcircle" /> Purger</button>
        </div>
      </div>

      {!audit.isLoading && !events.length ? (
        <EmptyState icon="history" title="Aucun événement audité">Les déploiements, secrets, backups et PR apparaîtront ici.</EmptyState>
      ) : (
        <div className="table-wrap">
          <table>
            <thead><tr><th>Heure</th><th>Acteur</th><th>Opération</th><th>Cible</th><th>Résultat</th><th>Détail</th></tr></thead>
            <tbody>
              {shown.map((e, i) => (
                <tr key={i}>
                  <td className="mono nowrap">{String(e.time || "").replace("T", " ").slice(0, 19)}</td>
                  <td>{e.actor || <span className="faint">–</span>}</td>
                  <td><div className="cell-stack"><b>{e.op}</b>{e.kind && <span className="muted" style={{ fontSize: 11 }}>{e.kind}{e.risk ? ` · ${e.risk}` : ""}</span>}</div></td>
                  <td className="mono">{e.target || <span className="faint">–</span>}</td>
                  <td><StateBadge kind={tone(e.result)}>{e.result}</StateBadge>{e.status ? <span className="faint" style={{ marginLeft: 6, fontSize: 11 }}>{e.status}</span> : null}</td>
                  <td className="muted" style={{ maxWidth: 360 }}>{e.error || e.message || (e.job_id ? <span className="mono">{e.job_id}</span> : e.commit ? <span className="mono">{String(e.commit).slice(0, 10)}</span> : "")}</td>
                </tr>
              ))}
              {!shown.length && <tr><td colSpan={6} className="muted">aucun événement ne correspond au filtre</td></tr>}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
