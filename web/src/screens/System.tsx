import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useSystem, usePostConfirm, usePlatform, useGenerations } from "../api/hooks";
import { Loading, pct, useMsg, MetricCard, SectionHead, StateBadge, Icon, useConfirm, Dialog, ActionButton } from "../components";
import { apiGet } from "../api/client";

// Read-only key/value rows for a config block.
function KV({ rows }: { rows: [string, any][] }) {
  return (
    <div className="table-wrap">
      <table><tbody>
        {rows.map(([k, v]) => (
          <tr key={k}><td style={{ width: 220, color: "var(--muted)" }}>{k}</td><td className="mono">{v === "" || v == null ? <span className="faint">—</span> : String(v)}</td></tr>
        ))}
      </tbody></table>
    </div>
  );
}

// Generation picker for OS rollback: lists real generations (number, date,
// version) instead of asking the operator to type a blind number.
function RollbackDialog({ onClose, onSubmit }: { onClose: () => void; onSubmit: (gen: string) => void }) {
  const gens = useGenerations();
  const list: any[] = (gens.data as any)?.generations || [];
  const [sel, setSel] = useState("");
  const candidates = list.filter((g) => !g.current);
  return (
    <Dialog title="Rollback génération NixOS" onClose={onClose}
      foot={<ActionButton variant="runtime" label="Rollback" onClick={() => sel && onSubmit(sel)} disabled={!sel} danger />}>
      {gens.isLoading ? <p className="muted">Chargement des générations…</p> : null}
      <label className="field"><span>Génération cible</span>
        <select autoFocus value={sel} onChange={(e) => setSel(e.target.value)}>
          <option value="">— choisir —</option>
          {candidates.map((g) => (
            <option key={g.number} value={String(g.number)}>
              #{g.number} · {String(g.date || "").slice(0, 16).replace("T", " ")} · {g.version || ""}
            </option>
          ))}
        </select>
      </label>
      <p className="muted" style={{ fontSize: 12 }}>
        Génération actuelle : #{list.find((g) => g.current)?.number ?? "?"}. Restaure une génération antérieure — double confirmation côté serveur.
      </p>
    </Dialog>
  );
}

export function System() {
  const sys = useSystem();
  const plat = usePlatform();
  const post = usePostConfirm(["system"]);
  const { setMsg, node } = useMsg();
  const { confirm, node: confirmNode } = useConfirm();
  const [rbOpen, setRbOpen] = useState(false);
  const s: any = sys.data || {};
  const p: any = plat.data || {};
  const disk = parseFloat(s.disk);

  // The server arms a double-confirm (HTTP 409 + confirm_id) on these risky
  // POSTs; usePostConfirm surfaces that as window.confirm(message) and re-POSTs
  // with confirm_id when accepted. r is undefined if the operator declines.
  const askConfirm = (m: string) => confirm(m, { danger: true, confirmLabel: "Confirmer" }).then((r) => r.ok);
  const deploy = () => {
    post.mutate({ path: "/v1/deployments", payload: { mode: "switch" }, confirm: askConfirm },
      { onSuccess: (r: any) => { if (r) setMsg({ text: "deploy lancé", ok: true }); }, onError: (e: any) => setMsg({ text: e.message, ok: false }) });
  };
  const rollback = (target: string) => {
    if (!/^[0-9]+$/.test(target.trim())) return;
    post.mutate({ path: "/v1/deployments", payload: { mode: "rollback", target: target.trim() }, confirm: askConfirm },
      { onSuccess: (r: any) => { if (r) setMsg({ text: "rollback lancé", ok: true }); }, onError: (e: any) => setMsg({ text: e.message, ok: false }) });
  };
  const [checking, setChecking] = useState(false);
  const queryClient = useQueryClient();
  // Drift state is cached server-side (15m TTL); ?refresh=1 forces a live
  // re-check against the remote, then the system query is refetched.
  const checkDrift = async () => {
    setChecking(true);
    try {
      await apiGet("/v1/drift?refresh=1");
      // DriftChip reads the ["drift"] query — refetching sys left it stale.
      await queryClient.invalidateQueries({ queryKey: ["drift"] });
      setMsg({ text: "Drift re-vérifié contre le remote", ok: true });
    } catch (e: any) {
      setMsg({ text: e.message, ok: false });
    } finally {
      setChecking(false);
    }
  };

  const reboot = () => {
    post.mutate({ path: "/v1/reboot", payload: {}, confirm: askConfirm },
      { onSuccess: (r: any) => { if (r) setMsg({ text: "reboot lancé", ok: true }); }, onError: (e: any) => setMsg({ text: e.message, ok: false }) });
  };

  return (
    <div>
      {node}
      <Loading q={sys} />

      <SectionHead title="Ressources" />
      <div className="grid">
        <MetricCard label="CPU" icon="cpu" value={pct(s.cpu)} gauge={parseFloat(s.cpu) || 0} />
        <MetricCard label="RAM" icon="ram" value={pct(s.mem)} gauge={parseFloat(s.mem) || 0} />
        <MetricCard label="Disque" icon="disk" value={pct(s.disk)} tone={disk > 90 ? "bad" : disk > 80 ? "warn" : "ok"} gauge={disk || 0} />
        <MetricCard label="Load (1m)" icon="monitoring" value={s.load1 ?? "–"} />
        <MetricCard label="Génération" icon="system" value={s.generation ?? "–"} sub={s.uptime_sec ? `up ${Math.floor(s.uptime_sec / 86400)}j ${Math.floor((s.uptime_sec % 86400) / 3600)}h` : undefined} />
      </div>

      <SectionHead title="Déploiement" />
      <div className="card pad-lg">
        <div className="row" style={{ justifyContent: "space-between" }}>
          <div className="cell-stack">
            <span className="muted">Commit déployé</span>
            <b className="mono" style={{ fontSize: 14 }}>{String(s.commit || "?").slice(0, 12)}</b>
          </div>
          <Icon name="refresh" className="muted" />
          <div className="cell-stack">
            <span className="muted">main</span>
            <b className="mono" style={{ fontSize: 14 }}>{String(s.main_commit || "?").slice(0, 12)}</b>
          </div>
          <div className="row" style={{ gap: 8 }}>
            {s.behind_main ? <StateBadge kind="risk">behind main</StateBadge> : <StateBadge kind="runtime-ok">à jour</StateBadge>}
            <button className="btn secondary sm" onClick={checkDrift} disabled={checking}>
              <Icon name="refresh" /> {checking ? "…" : "Vérifier"}
            </button>
          </div>
        </div>
      </div>

      <SectionHead title="Services infra" count={(s.infra || []).length} />
      {(s.infra || []).length ? (
        <div className="table-wrap">
          <table>
            <thead><tr><th>Service</th><th style={{ textAlign: "right" }}>État</th></tr></thead>
            <tbody>
              {(s.infra || []).map((i: any) => (
                <tr key={i.name}>
                  <td><b>{i.display_name || i.name}</b></td>
                  <td style={{ textAlign: "right" }}>
                    <StateBadge kind={i.state === "active" || i.state === "running" ? "runtime-ok" : "runtime-bad"}>{i.state}</StateBadge>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : <p className="muted">Aucun service infra remonté.</p>}

      <SectionHead title="Plateforme" sub="config/platform.nix (lecture seule — éditable dans Réglages)" />
      <div className="grid-2">
        <div className="card">
          <h3 style={{ margin: "0 0 8px" }}><Icon name="server" /> Hôte</h3>
          <KV rows={[["hostname", p.host?.hostname], ["timezone", p.host?.timezone], ["locale", p.host?.locale]]} />
        </div>
        <div className="card">
          <h3 style={{ margin: "0 0 8px" }}><Icon name="storage" /> Chemins & défauts</h3>
          <KV rows={[["dataRoot", p.paths?.dataRoot], ["secretsRoot", p.paths?.secretsRoot], ["defaultStorageClass", p.defaultStorageClass], ["updatePolicyDefault", p.updatePolicyDefault]]} />
        </div>
        <div className="card">
          <h3 style={{ margin: "0 0 8px" }}><Icon name="backups" /> Sauvegarde</h3>
          <KV rows={[["backend", p.backup?.backend], ["repository", p.backup?.repository], ["schedule", p.backup?.schedule]]} />
        </div>
        <div className="card">
          <h3 style={{ margin: "0 0 8px" }}><Icon name="monitoring" /> Observabilité</h3>
          <div className="row" style={{ marginBottom: 8 }}>
            <StateBadge kind="runtime-ok">interne · active</StateBadge>
          </div>
          <p className="muted" style={{ margin: 0, fontSize: 12 }}>
            Métriques collectées par control-api (hôte, infra du projet, apps). Détail dans <b>Monitoring</b>.
          </p>
        </div>
      </div>

      <div className="danger-zone">
        <div className="dz-head"><Icon name="octagon" /> Actions système sensibles</div>
        <div className="dz-text">
          Ces opérations modifient l'état réel de l'hôte et demandent une double confirmation. Le rollback restaure une génération NixOS antérieure.
        </div>
        <div className="row">
          <button className="btn solid-danger" onClick={deploy}><Icon name="play" /> Deploy main</button>
          <button className="btn danger" onClick={() => setRbOpen(true)}><Icon name="rollback" /> Rollback génération</button>
          <button className="btn danger" onClick={reboot}><Icon name="power" /> Reboot hôte</button>
        </div>
      </div>
      {confirmNode}
      {rbOpen && <RollbackDialog onClose={() => setRbOpen(false)} onSubmit={(g) => { rollback(g); setRbOpen(false); }} />}
    </div>
  );
}
