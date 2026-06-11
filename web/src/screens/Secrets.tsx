import { useState } from "react";
import { useSecrets, useSystemSecrets, usePost } from "../api/hooks";
import { ActionButton, Dialog, Loading, StateBadge, useMsg, AlertBanner, SectionHead, Icon } from "../components";

export function Secrets() {
  const sec = useSecrets();
  const sys = useSystemSecrets();
  const post = usePost(["secrets", "system-secrets", "changes"]);
  const { setMsg, node } = useMsg();
  const [edit, setEdit] = useState<{ app: string } | null>(null);
  const [kv, setKv] = useState<{ key: string; value: string }[]>([{ key: "", value: "" }]);
  const [sysEdit, setSysEdit] = useState<{ key: string; description: string } | null>(null);
  const [sysVal, setSysVal] = useState("");

  const sysSecrets: any[] = (sys.data as any)?.secrets || [];
  const closeSysEdit = () => { setSysEdit(null); setSysVal(""); };
  const submitSys = () => {
    if (!sysEdit || !sysVal.trim()) return;
    post.mutate(
      { path: "/v1/changes/system-secret", payload: { key: sysEdit.key, value: sysVal, reason: "set system secret via UI" } },
      {
        onSuccess: (r: any) => { setMsg({ text: r.pr?.url || "PR chiffrée créée", ok: true }); closeSysEdit(); },
        onError: (e: any) => setMsg({ text: e.message, ok: false }),
      },
    );
  };

  const apps: any[] = (sec.data as any)?.apps || [];
  const missing = (sec.data as any)?.missing_total ?? 0;

  // Clear typed values whenever the dialog closes so a secret never lingers in
  // the form state and reappears on the next open (even for another app).
  const closeEdit = () => { setEdit(null); setKv([{ key: "", value: "" }]); };

  const submit = () => {
    // Reject duplicate keys: silently keeping only the last value would let an
    // operator overwrite a secret without noticing.
    const keys = kv.map((p) => p.key).filter(Boolean);
    const dup = keys.find((k, i) => keys.indexOf(k) !== i);
    if (dup) { setMsg({ text: `Clé en double : ${dup}. Fusionnez ou supprimez le doublon.`, ok: false }); return; }
    const values: Record<string, string> = {};
    kv.forEach((p) => { if (p.key) values[p.key] = p.value; });
    if (!edit || !Object.keys(values).length) return;
    post.mutate(
      { path: "/v1/changes/app-secret", payload: { app: edit.app, values, reason: "Set/rotate secret" } },
      {
        onSuccess: (r: any) => { setMsg({ text: r.pr?.url || "PR chiffrée créée", ok: true }); closeEdit(); },
        onError: (e: any) => setMsg({ text: e.message, ok: false }),
      },
    );
  };

  return (
    <div>
      {node}
      {missing > 0
        ? <AlertBanner tone="warn" title={`${missing} secret(s) manquant(s)`}>Certaines apps attendent des secrets non encore configurés. Définissez-les via SOPS ci-dessous.</AlertBanner>
        : <AlertBanner tone="ok" title="Tous les secrets requis sont présents">Les valeurs ne sont jamais lues ni affichées par le control plane — seul le statut l'est.</AlertBanner>}
      <Loading q={sec} />

      <SectionHead title="Secrets système" count={sysSecrets.length} sub="hôte: backup, alertes, auth, tailnet — chiffrés sops, livrés par PR + deploy" />
      <div className="table-wrap" style={{ marginBottom: 18 }}>
        <table>
          <thead><tr><th>Clé</th><th>Rôle</th><th>Statut</th><th>Dernière rotation</th><th style={{ textAlign: "right" }}></th></tr></thead>
          <tbody>
            {sysSecrets.map((s: any) => (
              <tr key={s.key}>
                <td className="mono">{s.key}</td>
                <td className="muted">{s.description}</td>
                <td>{s.status === "present" ? <StateBadge kind="runtime-ok">présent</StateBadge> : <StateBadge kind="err">absent</StateBadge>}</td>
                <td className="mono">{s.rotated ? String(s.rotated).slice(0, 10) : <span className="faint">–</span>}</td>
                <td style={{ textAlign: "right" }}>
                  <ActionButton variant="pr" label={s.status === "present" ? "Faire tourner" : "Définir"} icon="secrets" onClick={() => setSysEdit({ key: s.key, description: s.description })} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <SectionHead title="Secrets par application" count={apps.length} />
      <div className="grid-2">
        {apps.map((a) => {
          const secrets = a.secrets || [];
          const miss = secrets.filter((s: any) => s.status === "missing").length;
          return (
            <div className="card" key={a.app}>
              <div className="row" style={{ justifyContent: "space-between", marginBottom: 10 }}>
                <b style={{ fontSize: 14, display: "flex", alignItems: "center", gap: 7 }}><Icon name="secrets" className="muted" />{a.app}</b>
                {miss ? <StateBadge kind="err">{miss} manquant(s)</StateBadge> : secrets.length ? <StateBadge kind="runtime-ok">complet</StateBadge> : <span className="faint">aucun secret</span>}
              </div>
              <div className="row" style={{ marginBottom: 12 }}>
                {secrets.length === 0 && <span className="muted">Cette app ne déclare aucun secret.</span>}
                {secrets.map((s: any) => (
                  <span key={s.name} className="row" style={{ gap: 5 }}>
                    <span className="mono" style={{ fontSize: 12 }}>{s.name}</span>
                    <StateBadge kind={s.status === "present" ? "runtime-ok" : s.status === "missing" ? "err" : "desired"}>{s.status}</StateBadge>
                  </span>
                ))}
              </div>
              <ActionButton variant="pr" label="Définir / faire tourner (SOPS)" icon="secrets" onClick={() => setEdit({ app: a.app })} />
            </div>
          );
        })}
        {!apps.length && <p className="muted">Aucune app.</p>}
      </div>

      {sysEdit && (
        <Dialog
          title={`Secret système · ${sysEdit.key}`}
          onClose={closeSysEdit}
          foot={<ActionButton variant="pr" label="Créer PR chiffrée" onClick={submitSys} disabled={!sysVal.trim()} />}
        >
          <AlertBanner tone="info" title={sysEdit.description}>
            Chiffré côté serveur (sops/age) vers <span className="mono">secrets/system/{sysEdit.key}.yaml</span>. Actif après merge + deploy.
          </AlertBanner>
          <label className="field"><span>Valeur</span>
            <input autoFocus type="password" value={sysVal} onChange={(e) => setSysVal(e.target.value)}
              placeholder={sysEdit.key === "oauth2_proxy_env" ? "OAUTH2_PROXY_CLIENT_ID=… (multi-lignes accepté)" : "valeur secrète"} />
          </label>
        </Dialog>
      )}

      {edit && (
        <Dialog
          title={`Secret chiffré · ${edit.app}`}
          onClose={closeEdit}
          foot={<ActionButton variant="pr" label="Créer PR chiffrée" onClick={submit} />}
        >
          <AlertBanner tone="info" title="Chiffrement côté serveur">Les valeurs sont chiffrées avec SOPS. La PR ne contient que du texte chiffré — jamais le secret en clair.</AlertBanner>
          {kv.map((p, i) => (
            <div className="row" key={i} style={{ marginBottom: 8 }}>
              <input placeholder="CLÉ" value={p.key} onChange={(e) => { const n = [...kv]; n[i].key = e.target.value; setKv(n); }} style={{ flex: 1 }} />
              <input placeholder="valeur" type="password" value={p.value} onChange={(e) => { const n = [...kv]; n[i].value = e.target.value; setKv(n); }} style={{ flex: 1 }} />
            </div>
          ))}
          <button className="btn secondary sm" onClick={() => setKv([...kv, { key: "", value: "" }])}><Icon name="plus" /> Ajouter une clé</button>
        </Dialog>
      )}
    </div>
  );
}
