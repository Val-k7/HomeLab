import { useState } from "react";
import { useBackups, usePost } from "../api/hooks";
import { Loading, StateBadge, useMsg, MetricCard, SectionHead, ActionMenu, Icon, Dialog, useConfirm, AlertBanner } from "../components";
import { apiGet } from "../api/client";

export function Backups() {
  const bk = useBackups();
  const post = usePost(["backups"]);
  const { setMsg, node } = useMsg();
  const { confirm, node: confirmNode } = useConfirm();
  const [logs, setLogs] = useState<string | null>(null);
  const apps: any[] = (bk.data as any)?.apps || [];

  const showLogs = async () => {
    try {
      const r: any = await apiGet(`/v1/backups/logs`);
      setLogs(r.logs || "(vide)");
    } catch (e: any) {
      setLogs(e?.message || "Erreur: impossible de charger les logs");
    }
  };

  const required = apps.filter((a) => a.backup_required);
  const covered = required.filter((a) => a.covered).length;
  const coverage = required.length ? Math.round((covered / required.length) * 100) : 100;
  const criticalApps = apps.filter((a) => a.criticality === "critical" || a.criticality === "high");
  const criticalProtected = criticalApps.filter((a) => a.covered).length;
  const restoreTested = apps.filter((a) => a.last_restore_test).length;
  const recentBackup = apps.filter((a) => a.last_backup).length;

  const run = async (op: string, app: string, snapshot = "") => {
    const message = op === "restore"
      ? `⚠ Restaurer ${app} (snapshot ${snapshot || "latest"}) ? Cela ÉCRASE les données actuelles de façon irréversible.`
      : op === "restore-test"
      ? `Test de restauration ${app} (bac à sable, sans écraser la prod) ?`
      : `Lancer « ${op} » sur ${app} ?`;
    if (!(await confirm(message, { danger: op === "restore", confirmLabel: op === "restore" ? "Restaurer" : "Lancer" })).ok) return;
    post.mutate({ path: `/v1/backups/${op}`, payload: { app, snapshot } },
      { onSuccess: (r: any) => setMsg({ text: `${op} ${app}: ${r.job_id || "ok"}`, ok: true }), onError: (e: any) => setMsg({ text: e.message, ok: false }) });
  };

  const [restoreApp, setRestoreApp] = useState<string | null>(null);
  const [snapId, setSnapId] = useState("");
  const configured = (bk.data as any)?.configured;
  const repoSet = (bk.data as any)?.repository_set;
  const pwSet = (bk.data as any)?.password_set;

  return (
    <div>
      {node}
      {confirmNode}
      <Loading q={bk} />

      {bk.data && configured === false ? (
        <AlertBanner tone="warn" title="Sauvegardes non configurées — aucun backup ne peut tourner">
          {!repoSet ? <>1. Définissez le dépôt restic dans <a href="#/settings">Réglages → Plateforme → Sauvegarde</a>. </> : null}
          {!pwSet ? <>{!repoSet ? "2." : "1."} Définissez le secret <span className="mono">restic_password</span> dans <a href="#/secrets">Secrets → Système</a> (PR + merge + deploy).</> : null}
        </AlertBanner>
      ) : null}

      <SectionHead title="Couverture" />
      <div className="grid">
        <MetricCard label="Couverture globale" icon="backups" value={`${coverage}%`} sub={`${covered}/${required.length} requis`} tone={coverage >= 100 ? "ok" : coverage >= 80 ? "warn" : "bad"} gauge={coverage} />
        <MetricCard label="Apps critiques protégées" icon="shield" value={`${criticalProtected}/${criticalApps.length}`} tone={criticalProtected === criticalApps.length ? "ok" : "bad"} />
        <MetricCard label="Backups récents" icon="clock" value={recentBackup} sub={`sur ${apps.length} apps`} />
        <MetricCard label="Restore tests" icon="check" value={restoreTested} tone={restoreTested ? "ok" : "warn"} />
      </div>

      <SectionHead title="Détail par application" count={apps.length} action={<button className="btn secondary sm" onClick={showLogs}><Icon name="terminal" /> Logs des jobs</button>} />
      <div className="table-wrap">
        <table>
          <thead><tr><th>App</th><th>Criticité</th><th>Requis</th><th>Couvert</th><th>Dernier backup</th><th>Restore test</th><th style={{ textAlign: "right" }}>Actions</th></tr></thead>
          <tbody>
            {apps.map((a) => (
              <tr key={a.app}>
                <td><b>{a.app}</b></td>
                <td><span className="chip">{a.criticality}</span></td>
                <td className="muted">{a.backup_required ? "oui" : "non"}</td>
                <td>{a.covered ? <StateBadge kind="runtime-ok">oui</StateBadge> : a.backup_required ? <StateBadge kind="err">non</StateBadge> : <span className="faint">n/a</span>}</td>
                <td className="mono">{a.last_backup ? String(a.last_backup).slice(0, 16) : <span className="faint">–</span>}</td>
                <td className="mono">{a.last_restore_test ? String(a.last_restore_test).slice(0, 16) : <span className="faint">–</span>}</td>
                <td>
                  <div className="actions" style={{ justifyContent: "flex-end" }}>
                    <button className="btn primary sm" onClick={() => run("run", a.app)}><Icon name="play" /> Sauvegarder</button>
                    <ActionMenu items={[
                      { label: "Test de restauration", icon: "rollback", onClick: () => run("restore-test", a.app) },
                      { label: "Vérifier l'intégrité", icon: "check", onClick: () => run("verify", a.app) },
                      { label: "Lister les snapshots", icon: "backups", onClick: () => run("snapshots", a.app) },
                      { label: "Voir les logs", icon: "terminal", onClick: showLogs },
                      "sep",
                      { label: "Restaurer (écrase les données)", icon: "download", danger: true, onClick: () => { setSnapId(""); setRestoreApp(a.app); } },
                    ]} />
                  </div>
                </td>
              </tr>
            ))}
            {!apps.length && <tr><td colSpan={7} className="muted">aucune app</td></tr>}
          </tbody>
        </table>
      </div>

      {restoreApp && (
        <Dialog title={`Restaurer · ${restoreApp}`} onClose={() => setRestoreApp(null)}
          foot={<button className="btn solid-danger" onClick={() => { const app = restoreApp; setRestoreApp(null); run("restore", app, snapId.trim()); }}><Icon name="download" /> Restaurer</button>}>
          <label className="field" style={{ marginBottom: 8 }}>
            <span>Snapshot restic (vide = latest)</span>
            <input autoFocus value={snapId} onChange={(e) => setSnapId(e.target.value)} placeholder="ex. 4f1c2ab8 — voir « Lister les snapshots » → Logs des jobs" />
          </label>
          <p className="muted" style={{ fontSize: 12 }}>Restaure dans <span className="mono">restore-tmp</span> puis écrase les données. Double confirmation côté serveur.</p>
        </Dialog>
      )}

      {logs != null && (
        <Dialog title="Logs des jobs de sauvegarde" onClose={() => setLogs(null)}>
          <div className="logbox"><div className="log-head"><Icon name="terminal" /> journalctl · hl-backup@*</div><pre>{logs}</pre></div>
        </Dialog>
      )}
    </div>
  );
}
