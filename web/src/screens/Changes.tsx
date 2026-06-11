import { useState, useEffect, useRef, useCallback } from "react";
import { useChanges } from "../api/hooks";
import { Loading, StateBadge, useMsg, AlertBanner, EmptyState, Icon, useConfirm, Dialog } from "../components";
import { apiGet, apiPost } from "../api/client";

function ciState(c: any): "success" | "failure" | "pending" | "" {
  const checks = c.github?.statusCheckRollup;
  if (!Array.isArray(checks)) return "";
  if (checks.some((x: any) => x.conclusion === "FAILURE" || x.state === "FAILURE")) return "failure";
  if (checks.every((x: any) => x.conclusion === "SUCCESS" || x.state === "SUCCESS")) return "success";
  return "pending";
}

// Normalise the GitHub status-check rollup (CheckRun and StatusContext nodes
// carry different field names) into a flat list with a link to each run's log.
function checkRuns(c: any): { name: string; state: string; url: string }[] {
  const checks = c.github?.statusCheckRollup;
  if (!Array.isArray(checks)) return [];
  return checks.map((x: any) => ({
    name: x.name || x.context || x.workflowName || "check",
    state: String(x.conclusion || x.state || x.status || "").toLowerCase(),
    url: x.detailsUrl || x.targetUrl || "",
  }));
}

function changeKey(c: any): string {
  return c.id || (c.pr_number != null ? `pr-${c.pr_number}` : `${c.type}-${c.title}`);
}

function ChangeDetail({
  c,
  onRetry,
  retrying,
  onMerge,
  merging,
  onClose,
  closing,
  onDiff,
}: {
  c: any;
  onRetry: (id: string) => void;
  retrying: boolean;
  onMerge: (id: string) => void;
  merging: boolean;
  onClose: (id: string) => void;
  closing: boolean;
  onDiff: (id: string) => void;
}) {
  const runs = checkRuns(c);
  const prHref = c.pr_url || c.compare_url;
  const retryable =
    c.branch &&
    ((!c.pr_url && (c.status === "failed" || c.status === "pushed")) ||
      c.github?.state === "CLOSED");
  const ci = ciState(c);
  const ghState = c.github?.state; // OPEN | CLOSED | MERGED (after refresh)
  const isOpenPR = !!c.pr_url && ghState === "OPEN";
  // Offer merge when GitHub reports the PR mergeable and the CI is not failing
  // or still running. A PR with no status checks (empty rollup) is mergeable
  // too — gh pr merge stays the final gate either way.
  const canMerge = isOpenPR && c.github?.mergeable === "MERGEABLE" && ci !== "failure" && ci !== "pending";
  const meta: [string, any][] = [
    ["ID", c.id],
    ["Auteur", c.actor],
    ["Date", c.time],
    ["Branche", c.branch],
    ["Commit", c.commit ? String(c.commit).slice(0, 12) : ""],
  ];
  return (
    <div className="change-detail">
      <div className="change-detail-actions">
        {prHref ? (
          <a className="btn pr" href={prHref} target="_blank" rel="noreferrer">
            <Icon name="external" /> {c.pr_url ? `Ouvrir la PR #${c.pr_number}` : "Ouvrir la PR (manuel)"}
          </a>
        ) : (
          <span className="faint">Aucune PR liée</span>
        )}
        {isOpenPR ? (
          ci === "success" ? (
            <span className="ci-pill ok"><Icon name="check" /> CI réussie</span>
          ) : ci === "failure" ? (
            <span className="ci-pill bad"><Icon name="xcircle" /> CI en échec</span>
          ) : (
            <span className="ci-pill pending"><Icon name="clock" /> CI en cours…</span>
          )
        ) : null}
        {isOpenPR ? (
          <button className="btn pr" onClick={() => onMerge(c.id)} disabled={merging || closing || !canMerge}
            title={canMerge ? "" : "CI en échec/en cours ou PR non mergeable"}>
            <Icon name="check" /> {merging ? "Merge…" : "Merger"}
          </button>
        ) : null}
        {isOpenPR ? (
          <button className="btn danger" onClick={() => onClose(c.id)} disabled={merging || closing}>
            <Icon name="xcircle" /> {closing ? "Fermeture…" : "Fermer la PR"}
          </button>
        ) : null}
        {retryable ? (
          <button className="btn secondary" onClick={() => onRetry(c.id)} disabled={retrying}>
            <Icon name="refresh" /> {retrying ? "…" : "Recréer la PR"}
          </button>
        ) : null}
        {c.pr_number ? (
          <button className="btn secondary" onClick={() => onDiff(c.id)}><Icon name="changes" /> Diff</button>
        ) : null}
      </div>

      <div className="kv-grid">
        {meta.filter(([, v]) => v).map(([k, v]) => (
          <div key={k}><span className="muted">{k}</span><span className="mono">{v}</span></div>
        ))}
      </div>

      {Array.isArray(c.files) && c.files.length ? (
        <div className="change-files">
          <div className="muted">Fichiers ({c.files.length})</div>
          <ul>{c.files.map((f: string) => <li key={f} className="mono">{f}</li>)}</ul>
        </div>
      ) : null}

      {runs.length ? (
        <div className="change-checks">
          <div className="muted">Checks CI</div>
          <ul>
            {runs.map((r, i) => (
              <li key={`${r.name}-${i}`}>
                <Icon name={r.state === "success" ? "check" : r.state === "failure" ? "xcircle" : "clock"} />
                <span className="mono">{r.name}</span>
                <span className="faint">{r.state || "–"}</span>
                {r.url ? <a href={r.url} target="_blank" rel="noreferrer"><Icon name="external" /> log</a> : null}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {c.github_warning || c.github_error ? (
        <p className="muted" style={{ fontSize: 12, display: "flex", gap: 6, alignItems: "center" }}>
          <Icon name="warn" /> {c.github_error || c.github_warning}
        </p>
      ) : null}

      {c.error ? (
        <div className="logbox">
          <div className="log-head"><Icon name="terminal" /> log d'erreur · {c.type}</div>
          <pre>{c.error}</pre>
        </div>
      ) : null}
    </div>
  );
}

export function Changes() {
  const ch = useChanges();
  const { setMsg, node } = useMsg();
  const { confirm, node: confirmNode } = useConfirm();
  const [refreshed, setRefreshed] = useState<any[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const [retrying, setRetrying] = useState<string | null>(null);
  const [merging, setMerging] = useState<string | null>(null);
  const [closing, setClosing] = useState<string | null>(null);
  const [diff, setDiff] = useState<{ pr: number; text: string } | null>(null);

  const showDiff = async (id: string) => {
    try {
      const r: any = await apiGet(`/v1/changes/diff?id=${id}`);
      setDiff({ pr: r.pr_number, text: r.diff + (r.truncated ? "\n… (tronqué)" : "") });
    } catch (e: any) {
      setMsg({ text: e.message, ok: false });
    }
  };

  // A change can appear several times in the log: each retry appends a fresh
  // record with the SAME id. Keep only the newest (records arrive newest-first)
  // so rows have unique keys — otherwise clicking one row expands every record
  // that shares its id.
  const raw: any[] = refreshed || (ch.data as any)?.changes || [];
  const seen = new Set<string>();
  const list: any[] = raw.filter((c) => {
    const k = changeKey(c);
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  });
  const failed = list.filter((c) => ciState(c) === "failure");

  // silent=true is used by the auto-poll below: refresh GitHub status without a
  // toast or the busy spinner, so a row's CI updates on its own.
  // mountedRef gates every setState in refresh: the polling interval below can
  // resolve a request after the screen unmounts (React warning + stale work).
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  const refresh = useCallback(async (silent = false) => {
    if (!silent) setBusy(true);
    try {
      const r: any = await apiPost("/v1/changes/refresh");
      if (!mountedRef.current) return;
      setRefreshed(r.changes || []);
      if (!silent) setMsg({ text: "Statuts GitHub rafraîchis", ok: true });
    } catch (e: any) {
      if (!silent && mountedRef.current) setMsg({ text: e.message, ok: false });
    } finally {
      if (!silent && mountedRef.current) setBusy(false);
    }
  }, [setMsg]);

  // Load GitHub status (CI / mergeable / review) as soon as the screen opens —
  // the CI and Revue columns would otherwise stay empty until the operator
  // clicks "Rafraîchir GitHub".
  useEffect(() => {
    refresh(true);
  }, [refresh]);

  // When a row is expanded on an open PR whose CI is still running (or whose
  // GitHub status hasn't loaded yet), poll the refresh endpoint so the CI pill
  // and the merge button update without the operator clicking "Rafraîchir".
  const openChange = list.find((c) => changeKey(c) === open);
  const ciPending =
    !!openChange?.pr_url &&
    openChange?.github?.state !== "MERGED" &&
    openChange?.github?.state !== "CLOSED" &&
    ciState(openChange) !== "success" &&
    ciState(openChange) !== "failure";
  const pollRef = useRef(false);
  useEffect(() => {
    if (!open) return;
    let active = true;
    // Load GitHub status immediately on expand, then poll every 12s while the
    // CI is still pending.
    if (!pollRef.current) {
      pollRef.current = true;
      refresh(true).finally(() => (pollRef.current = false));
    }
    const t = setInterval(() => {
      if (active && ciPending && !pollRef.current) {
        pollRef.current = true;
        refresh(true).finally(() => (pollRef.current = false));
      }
    }, 12000);
    return () => {
      active = false;
      clearInterval(t);
    };
  }, [open, ciPending, refresh]);

  // Resolved status: GitHub is the source of truth once known. A change whose
  // PR was closed/merged keeps a stale local status of "open"/"pushed"; show
  // the real GitHub state instead.
  const effStatus = (c: any): string => {
    const g = (c.github?.state || "").toLowerCase();
    return g === "closed" || g === "merged" ? g : c.status;
  };

  // Prune clears dead entries from the local log (admin): failures plus any
  // change whose PR is closed or merged on GitHub (matched by id, since the
  // stored status is stale).
  const prune = async () => {
    const deadIds = [...new Set(list.filter((c) => ["closed", "merged"].includes(effStatus(c))).map((c) => c.id))];
    const hasFailed = list.some((c) => c.status === "failed");
    if (!deadIds.length && !hasFailed) { setMsg({ text: "Rien à purger", ok: true }); return; }
    if (!(await confirm(`Purger les changements en échec et les PR fermées/mergées (${deadIds.length} terminée(s)) ?`, { danger: true, confirmLabel: "Purger" })).ok) return;
    try {
      const r: any = await apiPost("/v1/changes/prune", { status: hasFailed ? "failed" : "", ids: deadIds });
      setMsg({ text: `${r.pruned ?? 0} changement(s) purgé(s)`, ok: true });
      setRefreshed(null);
      ch.refetch?.();
    } catch (e: any) {
      setMsg({ text: e.message, ok: false });
    }
  };

  const close = async (id: string) => {
    if (!(await confirm("Fermer la PR sans merger ? La branche sera supprimée.", { danger: true, confirmLabel: "Fermer la PR" })).ok) return;
    setClosing(id);
    try {
      await apiPost("/v1/changes/close", { id });
      setMsg({ text: "PR fermée", ok: true });
      await refresh();
    } catch (e: any) {
      setMsg({ text: e.message, ok: false });
    } finally {
      setClosing(null);
    }
  };

  const merge = async (id: string) => {
    setMerging(id);
    try {
      await apiPost("/v1/changes/merge", { id });
      setMsg({ text: "PR mergée — déploiement déclenché", ok: true });
      await refresh();
    } catch (e: any) {
      setMsg({ text: e.message, ok: false });
    } finally {
      setMerging(null);
    }
  };

  const retry = async (id: string) => {
    setRetrying(id);
    try {
      const r: any = await apiPost("/v1/changes/retry", { id });
      setMsg({ text: r.pr?.url ? `PR #${r.pr.number} créée` : "PR recréée", ok: true });
      await refresh();
    } catch (e: any) {
      setMsg({ text: e.message, ok: false });
    } finally {
      setRetrying(null);
    }
  };

  return (
    <div>
      {node}
      {confirmNode}
      {diff && (
        <Dialog title={`Diff · PR #${diff.pr}`} onClose={() => setDiff(null)}>
          <div className="logbox"><div className="log-head"><Icon name="changes" /> gh pr diff</div><pre>{diff.text}</pre></div>
        </Dialog>
      )}
      <div className="section-head">
        <h3 style={{ margin: 0 }}>Pipeline GitOps <span className="count">· {list.length}</span></h3>
        <div className="row" style={{ gap: 0 }}>
          <button className="btn secondary" onClick={() => refresh()} disabled={busy}><Icon name="refresh" /> {busy ? "…" : "Rafraîchir GitHub"}</button>
          {list.some((c) => c.status === "failed" || ["closed", "merged"].includes(effStatus(c))) ? (
            <button className="btn danger" onClick={prune}><Icon name="xcircle" /> Purger terminés/échecs</button>
          ) : null}
        </div>
      </div>

      {failed.length ? (
        <AlertBanner tone="bad" title={`${failed.length} changement(s) en échec CI`}>
          {failed.map((c) => c.title).slice(0, 3).join(" · ")}{failed.length > 3 ? " …" : ""}
        </AlertBanner>
      ) : null}

      <Loading q={ch} />

      {!ch.isLoading && !list.length ? (
        <EmptyState icon="changes" title="Aucun changement en cours">
          Les PR créées depuis le control plane (install, update, secrets, storage) apparaîtront ici avec leur statut CI.
        </EmptyState>
      ) : list.length ? (
        <div className="table-wrap">
          <table>
            <thead><tr><th></th><th>Titre</th><th>Type</th><th>Statut</th><th>CI</th><th>Revue</th><th>PR</th></tr></thead>
            <tbody>
              {list.map((c) => {
                const gh = c.github || {};
                const ci = ciState(c);
                const key = changeKey(c);
                const isOpen = open === key;
                return [
                  <tr key={key} className="change-row" onClick={() => setOpen(isOpen ? null : key)}>
                    <td className="caret"><Icon name="chevron" className={isOpen ? "rot90" : ""} /></td>
                    <td><b>{c.title}</b></td>
                    <td className="muted">{c.type}</td>
                    <td><StateBadge kind={["closed", "merged"].includes(effStatus(c)) ? "desired" : "action"}>{effStatus(c)}</StateBadge></td>
                    <td>
                      {ci === "success" ? <StateBadge kind="runtime-ok">success</StateBadge>
                        : ci === "failure" ? <StateBadge kind="err">failure</StateBadge>
                        : ci === "pending" ? <StateBadge kind="risk">pending</StateBadge>
                        : <span className="faint">–</span>}
                    </td>
                    <td className="muted">{gh.reviewDecision || gh.mergeable || "–"}</td>
                    <td onClick={(e) => e.stopPropagation()}>
                      {c.pr_url ? <a href={c.pr_url} target="_blank" rel="noreferrer">#{c.pr_number}</a>
                        : c.compare_url ? <a href={c.compare_url} target="_blank" rel="noreferrer">manuel</a>
                        : <span className="faint">–</span>}
                    </td>
                  </tr>,
                  isOpen ? (
                    <tr key={`${key}-detail`} className="change-detail-row">
                      <td colSpan={7}><ChangeDetail c={c} onRetry={retry} retrying={retrying === c.id} onMerge={merge} merging={merging === c.id} onClose={close} closing={closing === c.id} onDiff={showDiff} /></td>
                    </tr>
                  ) : null,
                ];
              })}
            </tbody>
          </table>
        </div>
      ) : null}
    </div>
  );
}
