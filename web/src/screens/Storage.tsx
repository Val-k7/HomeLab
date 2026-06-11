import { useRef, useState } from "react";
import { useStorage, useStorageOrphans, usePostConfirm, usePost } from "../api/hooks";
import { Loading, StateBadge, useMsg, SectionHead, EmptyState, ActionButton, Icon, Dialog, useConfirm } from "../components";
import { AddStorageClassDialog } from "./AddStorageClassDialog";

export function Storage() {
  const st = useStorage();
  const post = usePost(["storage", "changes"]);
  const { setMsg, node } = useMsg();
  const { confirm, node: confirmNode } = useConfirm();
  const [addClass, setAddClass] = useState(false);
  const [move, setMove] = useState<{ app: string; volume: string; current: string } | null>(null);
  const moveClassRef = useRef<HTMLSelectElement>(null);
  const classes: any[] = (st.data as any)?.classes || [];
  const volumes: any[] = (st.data as any)?.volumes || [];
  const defaultClass: string = (st.data as any)?.default_class || "";
  const classNames = classes.map((c) => c.name);

  const usedClasses = new Set(volumes.map((v) => v.class));
  const orphansQ = useStorageOrphans();
  const orphans: any[] = (orphansQ.data as any)?.orphans || [];
  const postConfirm = usePostConfirm(["storage-orphans"]);
  const purgeOrphan = async (app: string) => {
    if (!(await confirm(`Purger définitivement les données résiduelles de « ${app} » ? Irréversible.`, { danger: true, confirmLabel: "Purger" })).ok) return;
    postConfirm.mutate(
      { path: "/v1/apps/purge-data", payload: { app }, confirm: (m) => confirm(m, { danger: true, confirmLabel: "Confirmer" }).then((r) => r.ok) },
      { onSuccess: (r: any) => { if (r) setMsg({ text: `Données de ${app} purgées`, ok: true }); }, onError: (e: any) => setMsg({ text: e.message, ok: false }) },
    );
  };
  const removeClass = async (name: string) => {
    if (!(await confirm(`Retirer la classe « ${name} » de platform.nix ? Une PR sera créée.`, { danger: true, confirmLabel: "Retirer" })).ok) return;
    post.mutate(
      { path: "/v1/changes/storage-class-remove", payload: { name, reason: `remove storage class ${name}` } },
      { onSuccess: (r: any) => setMsg({ text: r.pr?.url || "PR de retrait créée", ok: true }), onError: (e: any) => setMsg({ text: e.message, ok: false }) },
    );
  };

  const changeClass = (app: string, volume: string, target: string) =>
    post.mutate(
      { path: "/v1/changes/app-storage", payload: { app, volume, class: target, reason: `move ${app}/${volume} to ${target}` } },
      { onSuccess: (r: any) => setMsg({ text: r.pr?.url || "PR créée", ok: true }), onError: (e: any) => setMsg({ text: e.message, ok: false }) },
    );

  return (
    <div>
      {node}
      <Loading q={st} />

      <SectionHead title="Classes de stockage" count={classes.length}
        action={<button className="btn primary" onClick={() => setAddClass(true)}><Icon name="plus" /> Ajouter une classe</button>} />
      <div className="grid-2">
        {classes.map((c) => (
          <div className="card" key={c.name}>
            <div className="row" style={{ justifyContent: "space-between", marginBottom: 6 }}>
              <b style={{ fontSize: 14, display: "flex", alignItems: "center", gap: 7 }}><Icon name="storage" className="muted" />{c.name}</b>
              <span className="chip">{c.type}</span>
            </div>
            <div className="mono" style={{ marginBottom: 8 }}>{c.basePath}</div>
            <div className="row" style={{ justifyContent: "space-between" }}>
              <span className="row" style={{ gap: 6 }}>
                {c.backedUp ? <StateBadge kind="runtime-ok">sauvegardé</StateBadge> : <StateBadge kind="desired">non sauvegardé</StateBadge>}
                {c.ephemeral ? <StateBadge kind="risk">éphémère</StateBadge> : <StateBadge kind="info">persistant</StateBadge>}
              </span>
              {c.name === defaultClass
                ? <span className="faint" style={{ fontSize: 11 }}>défaut</span>
                : usedClasses.has(c.name)
                ? <span className="faint" style={{ fontSize: 11 }}>utilisée</span>
                : <button className="btn danger sm" onClick={() => removeClass(c.name)}>Retirer</button>}
            </div>
          </div>
        ))}
      </div>

      <SectionHead title="Volumes" count={volumes.length} />
      {volumes.length ? (
        <div className="table-wrap">
          <table>
            <thead><tr><th>App / Volume</th><th>Type</th><th>Classe</th><th>Chemin</th><th>Sauvegarde</th><th style={{ textAlign: "right" }}>Action</th></tr></thead>
            <tbody>
              {volumes.map((v) => {
                const critical = !v.backedUp && v.kind === "database";
                return (
                  <tr key={`${v.app}-${v.name}`}>
                    <td><div className="cell-stack"><b>{v.app}</b><span className="muted" style={{ fontSize: 12 }}>{v.name}</span></div></td>
                    <td className="muted">{v.kind}</td>
                    <td><span className="chip">{v.class}</span></td>
                    <td className="mono">{v.path}</td>
                    <td>{v.backedUp ? <StateBadge kind="runtime-ok">oui</StateBadge> : critical ? <StateBadge kind="err">DB non protégée</StateBadge> : <StateBadge kind="desired">non</StateBadge>}</td>
                    <td style={{ textAlign: "right" }}><ActionButton variant="pr" label="Changer classe" icon="storage" onClick={() => setMove({ app: v.app, volume: v.name, current: v.class })} /></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <EmptyState icon="storage" title="Aucun volume déclaré">Les volumes apparaissent quand une app déclare du stockage persistant.</EmptyState>
      )}
      {orphans.length ? (
        <>
          <SectionHead title="Données orphelines" count={orphans.length} sub="résidus d'apps supprimées — purgeables définitivement" />
          <div className="table-wrap">
            <table>
              <thead><tr><th>App (supprimée)</th><th>Chemin</th><th style={{ textAlign: "right" }}></th></tr></thead>
              <tbody>
                {orphans.map((o) => (
                  <tr key={o.path}>
                    <td><b>{o.app}</b></td>
                    <td className="mono">{o.path}</td>
                    <td style={{ textAlign: "right" }}><button className="btn danger sm" onClick={() => purgeOrphan(o.app)}>Purger</button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      ) : null}

      <p className="muted" style={{ display: "flex", gap: 8, alignItems: "center" }}><Icon name="info" /> Changer la classe d'un volume crée une PR structurée. Ajouter une classe édite <span className="mono">platform.nix</span> par PR.</p>
      {confirmNode}
      {addClass && <AddStorageClassDialog existing={classNames} onClose={() => setAddClass(false)} onResult={(t, ok) => setMsg({ text: t, ok })} />}
      {move && (
        <Dialog title={`Classe · ${move.app}/${move.volume}`} onClose={() => setMove(null)}
          foot={<ActionButton variant="pr" label="Changer (PR)" onClick={() => { const t = moveClassRef.current?.value; if (t && t !== move.current) changeClass(move.app, move.volume, t); setMove(null); }} />}>
          <label className="field"><span>Nouvelle classe</span>
            <select ref={moveClassRef} defaultValue={move.current}>
              {classNames.map((c) => <option key={c} value={c}>{c}</option>)}
            </select>
          </label>
        </Dialog>
      )}
    </div>
  );
}
