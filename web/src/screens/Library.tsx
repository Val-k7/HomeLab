import { useState } from "react";
import { useLibrary, usePost } from "../api/hooks";
import { ActionButton, Dialog, Loading, StateBadge, useMsg, EmptyState, SectionHead, Icon, useConfirm } from "../components";
import { apiGet, apiPost } from "../api/client";
import { AddCatalogDialog } from "./AddCatalogDialog";

export function Library() {
  const lib = useLibrary();
  const post = usePost(["library", "changes"]);
  const { setMsg, node } = useMsg();
  const { confirm, node: confirmNode } = useConfirm();
  const [browse, setBrowse] = useState<{ id: string; modules: any } | null>(null);
  const [editing, setEditing] = useState<any | null>(null);

  const catalogs: any[] = (lib.data as any)?.catalogs || [];
  const installed: any[] = (lib.data as any)?.installed || [];

  const removeCatalog = async (c: any) => {
    if (!(await confirm(`Retirer le catalogue « ${c.id} » ? Une PR sera créée; les modules déjà installés restent en place.`, { danger: true, confirmLabel: "Retirer" })).ok) return;
    apiPost("/v1/changes/catalog-remove", { id: c.id, reason: `remove catalog ${c.id}` })
      .then((r: any) => setMsg({ text: r.pr?.url || "PR de retrait créée", ok: true }))
      .catch((e: any) => setMsg({ text: e.message, ok: false }));
  };

  const openCatalog = async (id: string) => {
    try {
      const r: any = await apiGet(`/v1/library/catalog/${id}`);
      setBrowse({ id, modules: r.modules });
    } catch (e: any) {
      setMsg({ text: e.message, ok: false });
    }
  };

  // Drops the server-side clone cache then re-browses: picks up a force-pushed
  // tag or a fresh ref without waiting for the next deploy.
  const refreshCatalog = async (id: string) => {
    try {
      await apiPost("/v1/library/refresh", { id });
      setMsg({ text: `Cache du catalogue ${id} purgé — re-clone au prochain parcours`, ok: true });
    } catch (e: any) {
      setMsg({ text: e.message, ok: false });
    }
  };

  const [dlgMsg, setDlgMsg] = useState<{ text: string; ok: boolean } | null>(null);
  const install = (m: any, catalog: string) => {
    const payload = {
      // App name must be the module id ([a-z0-9-]); m.name is the human label
      // ("Image Demo (whoami)") and fails the server's name validation.
      name: m.id || m.name, catalog, module: m.id || m.name, version: m.version, repo: m.repo, sha: m.sha,
      hash: m.hash || "", runner: m.runner || "image", image: m.image || "", tag: m.tag || "", digest: m.digest || "",
      runtime: m.runtime || "", build_cmd: m.build_cmd || "", start_cmd: m.start_cmd || "", dir: m.dir || "",
      packages: m.packages || [], port: m.port || 0, container_port: m.container_port || 0, criticality: m.criticality || "low",
      permissions: m.permissions || [], volumes: m.volumes || [], secrets: m.secrets || [], reason: "Install from workshop",
    };
    setDlgMsg(null);
    post.mutate({ path: "/v1/changes/app-install", payload },
      {
        // Success closes the browse dialog so the toast is visible; an error is
        // shown INSIDE the dialog (a toast behind the overlay is unreadable).
        onSuccess: (r: any) => { setBrowse(null); setMsg({ text: r.pr?.url || "PR d'install créée", ok: true }); },
        onError: (e: any) => setDlgMsg({ text: e.message, ok: false }),
      });
  };

  return (
    <div>
      {node}
      {confirmNode}
      <Loading q={lib} />

      <SectionHead title="Catalogues" count={catalogs.length} />
      {catalogs.length ? (
        <div className="table-wrap">
          <table>
            <thead><tr><th>Nom</th><th>Catégorie</th><th>Dépôt</th><th>Réf</th><th>Confiance</th><th>Policy</th><th style={{ textAlign: "right" }}></th></tr></thead>
            <tbody>
              {catalogs.map((c) => (
                <tr key={c.id}>
                  <td><div className="cell-stack"><b>{c.name || c.id}</b>{c.description ? <span className="muted" style={{ fontSize: 12 }}>{c.description}</span> : null}</div></td>
                  <td className="muted">{c.category || "—"}</td>
                  <td className="mono">{c.repo}</td><td className="mono">{c.ref}</td>
                  <td><StateBadge kind="desired">{c.trust}</StateBadge></td>
                  <td><StateBadge kind={c.policy === "warn" ? "risk" : "runtime-ok"}>{c.policy || "strict"}</StateBadge></td>
                  <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                    <button className="btn secondary sm" onClick={() => openCatalog(c.id)}><Icon name="library" /> Parcourir</button>
                    <button className="btn secondary sm" onClick={() => refreshCatalog(c.id)} title="Purger le cache local du catalogue"><Icon name="refresh" /></button>
                    <button className="btn secondary sm" onClick={() => setEditing(c)}><Icon name="settings" /> Modifier</button>
                    <button className="btn danger sm" onClick={() => removeCatalog(c)}>Retirer</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <EmptyState icon="library" title="Aucun catalogue activé">
          Activez un catalogue workshop dans <span className="mono">config/catalogs.nix</span> pour parcourir et installer des modules.
          <div style={{ marginTop: 14 }}><a href="#/settings" className="btn primary"><Icon name="settings" /> Ouvrir les réglages</a></div>
        </EmptyState>
      )}

      <SectionHead title="Modules installés" count={installed.length} />
      {installed.length ? (
        <div className="table-wrap">
          <table>
            <thead><tr><th>Module</th><th>Catalogue</th><th>Version</th><th>SHA</th></tr></thead>
            <tbody>
              {installed.map((m) => (
                <tr key={`${m.catalog}-${m.module}`}><td><b>{m.module}</b></td><td className="muted">{m.catalog}</td><td>{m.version}</td><td className="mono">{String(m.sha || "").slice(0, 10)}</td></tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <EmptyState icon="inbox" title="Aucun module installé">Les modules installés depuis un catalogue apparaîtront ici.</EmptyState>
      )}

      {editing && (
        <AddCatalogDialog
          existing={catalogs.map((c) => c.id)}
          initial={{
            id: editing.id, repo: editing.repo || "", ref: editing.ref || "",
            trust: editing.trust || "community", policy: editing.policy || "strict",
            name: editing.name || "", description: editing.description || "", category: editing.category || "",
          }}
          onClose={() => setEditing(null)}
          onResult={(t, ok) => setMsg({ text: t, ok })}
        />
      )}

      {browse && (
        <Dialog title={`Catalogue · ${browse.id}`} onClose={() => { setBrowse(null); setDlgMsg(null); }}>
          {dlgMsg ? <div className={`msg ${dlgMsg.ok ? "ok" : "bad"}`}>{dlgMsg.text}</div> : null}
          {(Array.isArray(browse.modules) ? browse.modules : browse.modules?.modules || []).map((m: any) => (
            <div key={m.id || m.name} className="card" style={{ marginBottom: 10 }}>
              <div className="row" style={{ justifyContent: "space-between" }}>
                <b style={{ fontSize: 14 }}>{m.name || m.id}</b>
                <span className="mono muted">{m.version}</span>
              </div>
              <div className="muted" style={{ fontSize: 12, margin: "4px 0 8px" }}>{m.description}</div>
              <div className="row" style={{ marginBottom: 10 }}>
                <span className="faint" style={{ fontSize: 12 }}>Demande :</span>
                {(m.permissions || []).map((p: string) => <span key={p} className="chip warn">{p}</span>)}
                {(m.secrets || []).length ? <span className="chip">{(m.secrets || []).length} secret(s)</span> : null}
                {(m.volumes || []).length ? <span className="chip">{(m.volumes || []).length} volume(s)</span> : null}
              </div>
              <ActionButton variant="pr" label="Installer (PR)" icon="plus" onClick={() => install(m, browse.id)} />
            </div>
          ))}
        </Dialog>
      )}
    </div>
  );
}
