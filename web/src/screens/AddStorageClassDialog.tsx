import { useEffect, useState } from "react";
import { apiGet, apiPost } from "../api/client";
import { ActionButton, Dialog, Icon, AlertBanner } from "../components";

// Structured storage-class add. The server (/v1/changes/storage-class)
// validates every field (allowlist regexes, nixString escaping) and generates
// + splices the entry into config/platform.nix itself — the client never
// submits generated Nix. The file is fetched here only to warn early when the
// storageClasses block is missing; the preview below mirrors the server
// output. Non-standard files go through the raw editor (Réglages).
const PLATFORM_PATH = "config/platform.nix";
const TYPES = ["local", "nfs", "ssd", "tmpfs"];
const reName = /^[a-zA-Z_][a-zA-Z0-9_-]{0,39}$/;
const BLOCK = /storageClasses = \{\n/;
// Mirror of control-api nixString (escapes \ " $) so the preview matches what
// the server generates and stays inert Nix.
const nixStr = (s: string) => `"${s.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\$/g, "\\$")}"`;

export function AddStorageClassDialog({ existing, onClose, onResult }: {
  existing: string[]; onClose: () => void; onResult: (t: string, ok: boolean) => void;
}) {
  const [base, setBase] = useState<string | null>(null);
  const [loadErr, setLoadErr] = useState("");
  const [name, setName] = useState("");
  const [type, setType] = useState("local");
  const [basePath, setBasePath] = useState("");
  const [backedUp, setBackedUp] = useState(true);
  const [ephemeral, setEphemeral] = useState(false);
  const [backupRepo, setBackupRepo] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    apiGet(`/v1/configfile?path=${encodeURIComponent(PLATFORM_PATH)}`)
      .then((r: any) => setBase(r.content || ""))
      .catch((e: any) => setLoadErr(e.message));
  }, []);

  const repoAttr = backedUp && backupRepo.trim() ? ` backupRepo = ${nixStr(backupRepo.trim())};` : "";
  const entry = `    ${reName.test(name) ? name : "<nom>"} = { type = ${nixStr(type)}; basePath = ${basePath.trim() ? nixStr(basePath.trim()) : '"<chemin>"'}; backedUp = ${backedUp};${ephemeral ? " ephemeral = true;" : ""}${repoAttr} };`;
  const dup = existing.includes(name);
  const valid = reName.test(name) && basePath.trim().startsWith("/") && !dup;
  const ready = valid && base != null && BLOCK.test(base);

  const submit = () => {
    if (!ready) return;
    setBusy(true);
    apiPost("/v1/changes/storage-class", {
      name, type, base_path: basePath.trim(), backed_up: backedUp, ephemeral,
      backup_repo: backedUp ? backupRepo.trim() : "", reason: `add storage class ${name}`,
    })
      .then((r: any) => { onResult(r.pr?.url || "PR de classe créée", true); onClose(); })
      .catch((e: any) => { onResult(e.message, false); setBusy(false); });
  };

  return (
    <Dialog title="Nouvelle classe de stockage" onClose={onClose} foot={
      <ActionButton variant="pr" label="Créer la PR" onClick={submit} disabled={!ready || busy} />
    }>
      {loadErr ? <AlertBanner tone="bad" title="Lecture de platform.nix impossible">{loadErr}</AlertBanner> : null}
      {base != null && !BLOCK.test(base) ? (
        <AlertBanner tone="warn" title="Bloc storageClasses introuvable">
          Format non standard — éditez <span className="mono">config/platform.nix</span> directement dans Réglages.
        </AlertBanner>
      ) : null}

      <div className="row" style={{ marginBottom: 8 }}>
        <label className="field" style={{ flex: 1 }}>Nom
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="ex. archive" maxLength={40} />
        </label>
        <label className="field">Type
          <select value={type} onChange={(e) => setType(e.target.value)}>{TYPES.map((t) => <option key={t}>{t}</option>)}</select>
        </label>
      </div>
      <label className="field" style={{ marginBottom: 8 }}>basePath
        <input value={basePath} onChange={(e) => setBasePath(e.target.value)} placeholder="/mnt/archive" maxLength={200} />
      </label>
      <div className="row" style={{ marginBottom: 10 }}>
        <label className="row" style={{ gap: 6 }}><input type="checkbox" checked={backedUp} onChange={(e) => setBackedUp(e.target.checked)} /> sauvegardé</label>
        <label className="row" style={{ gap: 6 }}><input type="checkbox" checked={ephemeral} onChange={(e) => setEphemeral(e.target.checked)} /> éphémère (tmpfs)</label>
      </div>
      {backedUp && (
        <label className="field" style={{ marginBottom: 10 }}>Destination de sauvegarde <span className="faint">(repo restic, optionnel — défaut = backup global)</span>
          <input value={backupRepo} onChange={(e) => setBackupRepo(e.target.value)} placeholder="ex. s3:s3.amazonaws.com/bucket ou /mnt/backup" maxLength={300} />
        </label>
      )}
      {dup ? <p className="msg bad">Une classe nommée « {name} » existe déjà.</p> : null}

      <div className="muted" style={{ fontSize: 12, marginBottom: 4, display: "flex", alignItems: "center", gap: 6 }}><Icon name="storage" /> Entrée générée :</div>
      <pre>{entry}</pre>
    </Dialog>
  );
}
