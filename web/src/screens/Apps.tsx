import { useState } from "react";
import { useAppsState, usePost, useUpdates } from "../api/hooks";
import { ActionButton, ActionMenu, Dialog, EmptyState, Icon, Loading, StateBadge, useMsg, useConfirm } from "../components";
import { apiGet, apiPost } from "../api/client";
import { AddAppDialog } from "./AddAppDialog";

const CRITICALITY = ["low", "medium", "high", "critical"];
const UPDATE_POLICY = ["manual", "autoLow", "critical"];

// Edit an app's criticality / update policy. The endpoint takes ONE key per
// call, so we fire a separate PR-change for each field the operator changed.
function PolicyDialog({ app, current, onClose, onResult }: {
  app: string; current: { criticality?: string; updatePolicy?: string };
  onClose: () => void; onResult: (t: string, ok: boolean) => void;
}) {
  const post = usePost(["apps-state", "changes"]);
  const [crit, setCrit] = useState(current.criticality || "low");
  const [upd, setUpd] = useState(current.updatePolicy || "manual");

  const save = () => {
    const calls: { field: string; payload: any }[] = [];
    if (crit !== (current.criticality || "low")) calls.push({ field: "criticality", payload: { app, criticality: crit, reason: `set criticality ${crit}` } });
    if (upd !== (current.updatePolicy || "manual")) calls.push({ field: "update_policy", payload: { app, update_policy: upd, reason: `set update policy ${upd}` } });
    if (!calls.length) { onClose(); return; }
    calls.forEach((c) => post.mutate(
      { path: "/v1/changes/app-policy", payload: c.payload },
      { onSuccess: (r: any) => onResult(r.pr?.url || `PR ${c.field} créée`, true), onError: (e: any) => onResult(e.message, false) },
    ));
    onClose();
  };

  return (
    <Dialog title={`Policy · ${app}`} onClose={onClose} foot={<ActionButton variant="pr" label="Appliquer (PR)" onClick={save} />}>
      <div className="row" style={{ marginBottom: 4 }}>
        <label className="field" style={{ flex: 1 }}>Criticité
          <select value={crit} onChange={(e) => setCrit(e.target.value)}>{CRITICALITY.map((c) => <option key={c}>{c}</option>)}</select>
        </label>
        <label className="field" style={{ flex: 1 }}>Politique de mise à jour
          <select value={upd} onChange={(e) => setUpd(e.target.value)}>{UPDATE_POLICY.map((u) => <option key={u}>{u}</option>)}</select>
        </label>
      </div>
      <p className="muted">Chaque champ modifié crée une PR distincte sur <span className="mono">apps/{app}.nix</span>.</p>
    </Dialog>
  );
}

function RollbackDialog({ app, onClose, onSubmit }: { app: string; onClose: () => void; onSubmit: (target: string, reason: string) => void }) {
  const [target, setTarget] = useState("");
  const [reason, setReason] = useState("");
  return (
    <Dialog title={`Rollback · ${app}`} onClose={onClose}
      foot={<ActionButton variant="pr" label="Rollback (PR)" onClick={() => target.trim() && onSubmit(target.trim(), reason.trim())} disabled={!target.trim()} />}>
      <label className="field" style={{ marginBottom: 8 }}><span>Cible (digest / tag / rev)</span>
        <input autoFocus value={target} onChange={(e) => setTarget(e.target.value)} placeholder="sha256:… ou v1.2.3 ou commit" />
      </label>
      <label className="field"><span>Raison</span>
        <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="optionnel" />
      </label>
    </Dialog>
  );
}

export function Apps() {
  const apps = useAppsState();
  const updates = useUpdates();
  const post = usePost(["apps-state", "changes"]);
  const { setMsg, node } = useMsg();
  const { confirm, node: confirmNode } = useConfirm();
  const [logs, setLogs] = useState<{ app: string; text: string } | null>(null);
  const [adding, setAdding] = useState(false);
  const [policy, setPolicy] = useState<{ app: string; current: any } | null>(null);

  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);

  const all: any[] = (apps.data as any)?.apps || [];
  const q = search.trim().toLowerCase();
  const list: any[] = q
    ? all.filter((a) => `${a.name} ${a.desired?.runner || ""} ${a.desired?.criticality || ""}`.toLowerCase().includes(q))
    : all;
  const upd: any[] = ((updates.data as any)?.updates || []).filter((u: any) => u.behind);

  const toggle = (name: string) =>
    setSelected((s) => { const n = new Set(s); n.has(name) ? n.delete(name) : n.add(name); return n; });
  const toggleAll = () =>
    setSelected((s) => (s.size === list.length ? new Set() : new Set(list.map((a) => a.name))));

  // Bulk ops run sequentially: /v1/action restarts go through systemd one by
  // one and a summary toast reports failures instead of one toast per app.
  const bulk = async (op: "restart" | "health") => {
    const names = [...selected];
    if (!names.length) return;
    if (op === "restart") {
      const r = await confirm(`Redémarrer ${names.length} app(s) : ${names.join(", ")} ?`, { confirmLabel: "Redémarrer" });
      if (!r.ok) return;
    }
    setBulkBusy(true);
    let ok = 0;
    const fails: string[] = [];
    for (const name of names) {
      try {
        if (op === "restart") await apiPost("/v1/action", { kind: "service", target: `app-${name}.service`, op: "restart" });
        else {
          const r: any = await apiPost("/v1/health/check", { app: name });
          if (!r.healthy) { fails.push(`${name} (down)`); continue; }
        }
        ok++;
      } catch (e: any) {
        fails.push(name);
      }
    }
    setBulkBusy(false);
    setSelected(new Set());
    setMsg(fails.length
      ? { text: `${ok} OK · échecs : ${fails.join(", ")}`, ok: false }
      : { text: `${ok}/${names.length} ${op === "restart" ? "redémarrée(s)" : "healthy"}`, ok: true });
  };

  const updatePR = (app: string, target: string) =>
    post.mutate(
      { path: "/v1/changes/app-update", payload: { app, target, reason: "Update from control plane" } },
      { onSuccess: (r: any) => setMsg({ text: r.pr?.url || "PR créée", ok: true }), onError: (e: any) => setMsg({ text: e.message, ok: false }) },
    );
  const [rollbackApp, setRollbackApp] = useState<string | null>(null);
  const [updateTo, setUpdateTo] = useState<string | null>(null);
  const rollbackPR = (app: string, target: string, reason: string) =>
    post.mutate(
      { path: "/v1/changes/app-rollback", payload: { app, target, reason: reason || "rollback" } },
      { onSuccess: (r: any) => setMsg({ text: r.pr?.url || "PR créée", ok: true }), onError: (e: any) => setMsg({ text: e.message, ok: false }) },
    );
  const removePR = async (app: string) => {
    const r = await confirm(`Supprimer l'app ${app} ? Une PR de suppression de apps/${app}.nix sera créée (revue + merge requis).`,
      { danger: true, reason: true, reasonLabel: "Raison de la suppression", confirmLabel: "Supprimer" });
    if (!r.ok) return;
    post.mutate(
      { path: "/v1/changes/app-remove", payload: { app, reason: r.reason || "remove app" } },
      { onSuccess: (x: any) => setMsg({ text: x.pr?.url || "PR de suppression créée", ok: true }), onError: (e: any) => setMsg({ text: e.message, ok: false }) },
    );
  };
  const restart = async (app: string) => {
    const r = await confirm(`Redémarrer ${app} ?`, { confirmLabel: "Redémarrer" });
    if (!r.ok) return;
    post.mutate(
      { path: "/v1/action", payload: { kind: "service", target: `app-${app}.service`, op: "restart" } },
      { onSuccess: () => setMsg({ text: `${app} restart envoyé`, ok: true }), onError: (e: any) => setMsg({ text: e.message, ok: false }) },
    );
  };
  const healthcheck = (app: string) =>
    post.mutate(
      { path: "/v1/health/check", payload: { app } },
      { onSuccess: (r: any) => setMsg({ text: `${app}: ${r.healthy ? "healthy" : "down"} (${r.detail})`, ok: r.healthy }), onError: (e: any) => setMsg({ text: e.message, ok: false }) },
    );
  const showLogs = async (app: string) => {
    try {
      const r: any = await apiGet(`/v1/logs?app=${encodeURIComponent(app)}`);
      setLogs({ app, text: r.logs || "(vide)" });
    } catch (e: any) {
      setMsg({ text: e.message, ok: false });
    }
  };

  return (
    <div>
      {node}
      <div className="section-head" style={{ marginTop: 0 }}>
        <h3 style={{ margin: 0 }}>Applications <span className="count">· {list.length}{q ? ` / ${all.length}` : ""}</span></h3>
        <div className="row">
          <input placeholder="Rechercher (nom, runner, criticité…)" value={search} onChange={(e) => setSearch(e.target.value)} style={{ minWidth: 220 }} />
          <button className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" /> Nouvelle app</button>
        </div>
      </div>

      {selected.size ? (
        <div className="alert info" style={{ alignItems: "center" }}>
          <Icon name="apps" />
          <div className="a-body row" style={{ alignItems: "center", gap: 10 }}>
            <b>{selected.size} app(s) sélectionnée(s)</b>
            <button className="btn secondary sm" onClick={() => bulk("restart")} disabled={bulkBusy}><Icon name="restart" /> Redémarrer</button>
            <button className="btn secondary sm" onClick={() => bulk("health")} disabled={bulkBusy}><Icon name="heart" /> Healthcheck</button>
            <button className="btn secondary sm" onClick={() => setSelected(new Set())} disabled={bulkBusy}>Annuler</button>
            {bulkBusy ? <span className="muted">en cours…</span> : null}
          </div>
        </div>
      ) : null}
      <Loading q={apps} />

      {upd.length ? (
        <div className="alert info">
          <Icon name="download" />
          <div className="a-body">
            <div className="a-title">{upd.length} mise(s) à jour disponible(s)</div>
            <div className="row" style={{ marginTop: 8 }}>
              {upd.map((u) => (
                <span key={u.app} className="row" style={{ gap: 6 }}>
                  <span className="mono" style={{ fontSize: 12 }}>{u.app}</span>
                  <span className="faint" style={{ fontSize: 11 }}>{String(u.current).slice(0, 8)} → {String(u.latest).slice(0, 8)}</span>
                  <button className="btn pr sm" onClick={() => updatePR(u.app, u.latest)}><Icon name="changes" /> Màj</button>
                </span>
              ))}
            </div>
          </div>
        </div>
      ) : null}

      {!apps.isLoading && !list.length ? (
        <EmptyState icon="apps" title="Aucune application déployée">
          Créez une app depuis une image, un compose ou un repo git — ou installez un module du catalogue.
          <div className="row" style={{ justifyContent: "center", marginTop: 14 }}>
            <button className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" /> Nouvelle app</button>
            <a href="#/library" className="btn secondary"><Icon name="library" /> Bibliothèque</a>
          </div>
        </EmptyState>
      ) : list.length ? (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th style={{ width: 28 }}><input type="checkbox" checked={!!list.length && selected.size === list.length} onChange={toggleAll} title="Tout sélectionner" aria-label="Tout sélectionner" /></th>
                <th>App</th><th>Runtime</th><th>Drift</th><th>Secrets</th><th>Sauvegarde</th><th>Policy</th><th style={{ textAlign: "right" }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {list.map((a) => {
                const d = a.desired || {}, rt = a.runtime || {};
                const sec = a.secrets_status?.summary || {};
                const bk = a.backup_status || {};
                const errs = (a.policy_status?.violations || []).filter((v: any) => v.severity === "error").length;
                const canUpdate = !!(d.tag || d.rev);
                return (
                  <tr key={a.name}>
                    <td><input type="checkbox" checked={selected.has(a.name)} onChange={() => toggle(a.name)} /></td>
                    <td>
                      <div className="cell-stack">
                        <b>{a.name}</b>
                        <span className="muted" style={{ fontSize: 12 }}>{d.runner} · {String(d.version || "").slice(0, 14)}</span>
                        <span style={{ display: "flex", gap: 6, flexWrap: "wrap", marginTop: 2 }}>
                          {d.criticality && <span className="chip">{d.criticality}</span>}
                          {d.updatePolicy && <span className="chip">{d.updatePolicy}</span>}
                        </span>
                        {d.dependencies?.length ? <span className="faint" style={{ fontSize: 11 }}>après : {d.dependencies.join(", ")}</span> : null}
                        {d.healthcheck?.type ? <span className="faint" style={{ fontSize: 11 }}>health : {d.healthcheck.type}{d.healthcheck.path ? ` ${d.healthcheck.path}` : ""}</span> : null}
                      </div>
                    </td>
                    <td>
                      <div className="cell-stack">
                        {rt.present
                          ? <StateBadge kind={rt.state === "active" ? "runtime-ok" : "runtime-bad"}>{rt.state}</StateBadge>
                          : <StateBadge kind="runtime-bad">absent</StateBadge>}
                        {rt.sub && rt.sub !== rt.state ? <span className="faint" style={{ fontSize: 11 }}>{rt.sub}</span> : null}
                      </div>
                    </td>
                    <td>{a.drift ? <StateBadge kind="risk">drift</StateBadge> : <span className="faint">ok</span>}</td>
                    <td>{sec.missing ? <StateBadge kind="err">{sec.missing} miss</StateBadge> : <span className="faint">ok</span>}</td>
                    <td>{bk.covered === false ? <StateBadge kind="err">gap</StateBadge> : bk.last_backup ? <span className="mono">{String(bk.last_backup).slice(0, 10)}</span> : <span className="faint">–</span>}</td>
                    <td>{errs ? <StateBadge kind="err">{errs} err</StateBadge> : <span className="faint">ok</span>}</td>
                    <td>
                      <div className="actions" style={{ justifyContent: "flex-end" }}>
                        {d.port ? (
                          <a href={`${location.protocol}//${location.hostname}:${d.port}`} target="_blank" rel="noreferrer" className="btn primary"><Icon name="external" /> Ouvrir</a>
                        ) : null}
                        <button className="btn secondary sm" onClick={() => showLogs(a.name)}><Icon name="terminal" /> Logs</button>
                        <ActionMenu items={[
                          { heading: "Runtime (audité)" },
                          { label: "Redémarrer", icon: "restart", onClick: () => restart(a.name) },
                          { label: "Healthcheck", icon: "heart", onClick: () => healthcheck(a.name) },
                          "sep",
                          { heading: "Via PR" },
                          { label: "Modifier la policy", icon: "security", onClick: () => setPolicy({ app: a.name, current: { criticality: d.criticality, updatePolicy: d.updatePolicy } }) },
                          ...(canUpdate ? [{ label: "Mettre à jour", icon: "changes", onClick: () => updatePR(a.name, d.tag || d.rev) }] : []),
                          { label: "Mettre à jour vers…", icon: "download", onClick: () => setUpdateTo(a.name) },
                          { label: "Rollback", icon: "rollback", danger: true, onClick: () => setRollbackApp(a.name) },
                          { label: "Supprimer l'app", icon: "xcircle", danger: true, onClick: () => removePR(a.name) },
                        ]} />
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : null}

      {confirmNode}
      {rollbackApp && <RollbackDialog app={rollbackApp} onClose={() => setRollbackApp(null)} onSubmit={(target, reason) => { rollbackPR(rollbackApp, target, reason); setRollbackApp(null); }} />}
      {updateTo && (
        <Dialog title={`Mettre à jour · ${updateTo}`} onClose={() => setUpdateTo(null)}
          foot={<ActionButton variant="pr" label="Mettre à jour (PR)" onClick={() => { const t = (document.getElementById("upd-target") as HTMLInputElement)?.value.trim(); if (t) { updatePR(updateTo, t); setUpdateTo(null); } }} />}>
          <label className="field"><span>Version cible (tag / digest / rev)</span>
            <input id="upd-target" autoFocus placeholder="ex. v1.12.0, sha256:…, ou commit" />
          </label>
          <p className="muted" style={{ fontSize: 12 }}>Crée une PR sur <span className="mono">apps/{updateTo}.nix</span> — version arbitraire, pas seulement la dernière détectée.</p>
        </Dialog>
      )}
      {adding && <AddAppDialog onClose={() => setAdding(false)} onResult={(t, ok) => setMsg({ text: t, ok })} />}
      {policy && <PolicyDialog app={policy.app} current={policy.current} onClose={() => setPolicy(null)} onResult={(t, ok) => setMsg({ text: t, ok })} />}
      {logs && (
        <Dialog title={`Logs · ${logs.app}`} onClose={() => setLogs(null)}>
          <div className="logbox"><div className="log-head"><Icon name="terminal" /> journalctl · {logs.app}</div><pre>{logs.text}</pre></div>
        </Dialog>
      )}
    </div>
  );
}
