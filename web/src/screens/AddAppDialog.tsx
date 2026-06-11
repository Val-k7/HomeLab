import { useState } from "react";
import { usePost } from "../api/hooks";
import { apiPost } from "../api/client";
import { ActionButton, Dialog, Icon, AlertBanner } from "../components";

// Manual app creation — the control-api supports four runners that the catalog
// install path never exposed. mode drives which fields are required (mirrors
// generateAppFiles validation server-side); a dry-run preview (app-add/preview)
// shows the generated Nix + risk before the PR is created.
type Mode = "image" | "compose" | "process" | "dockerfile";

const MODES: { key: Mode; label: string; hint: string }[] = [
  { key: "image", label: "Image OCI", hint: "image + tag (ex. ghcr.io/org/app:1.2)" },
  { key: "compose", label: "Docker Compose", hint: "compose en ligne, ou tiré d'un repo git (rev + chemin)" },
  { key: "process", label: "Processus (build git)", hint: "repo + rev + runtime + build/start" },
  { key: "dockerfile", label: "Dockerfile (git)", hint: "repo + rev, build d'un Dockerfile" },
];

type Preview = { ok?: boolean; summary?: string; risk?: string; warnings?: string[]; checks?: string[]; files?: { path: string }[] };

// Module-level so its identity is stable across renders — a component declared
// inside AddAppDialog would remount every keystroke and steal input focus.
function Field({ label, value, onChange, ph, type = "text" }: { label: string; value: string; onChange: (e: any) => void; ph?: string; type?: string }) {
  return (
    <label className="field" style={{ flex: 1, minWidth: 180 }}>{label}
      <input value={value} onChange={onChange} placeholder={ph} type={type} />
    </label>
  );
}

export function AddAppDialog({ onClose, onResult }: { onClose: () => void; onResult: (text: string, ok: boolean) => void }) {
  const create = usePost(["apps-state", "changes"]);
  const [mode, setMode] = useState<Mode>("image");
  const [f, setF] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<Preview | null>(null);
  const [busy, setBusy] = useState(false);
  const set = (k: string) => (e: any) => { setF((s) => ({ ...s, [k]: e.target.value })); setPreview(null); };

  const payload = () => ({
    name: f.name || "",
    mode,
    image: f.image || "",
    tag: f.tag || "",
    repo: f.repo || "",
    rev: f.rev || "",
    runtime: f.runtime || "",
    build_cmd: f.build_cmd || "",
    start_cmd: f.start_cmd || "",
    dir: f.dir || "",
    compose: f.compose || "",
    port: parseInt(f.port || "0", 10) || 0,
    packages: (f.packages || "").split(/[\s,]+/).filter(Boolean),
    env_file: f.env_file || "",
    reason: f.reason || "Add app from control plane",
  });

  const doPreview = async () => {
    setBusy(true);
    try {
      const r: any = await apiPost("/v1/changes/app-add/preview", payload());
      setPreview(r);
    } catch (e: any) {
      setPreview(null); // drop the stale preview — it no longer matches the form
      onResult(e.message, false);
    } finally { setBusy(false); }
  };

  const doCreate = () => {
    setBusy(true);
    create.mutate({ path: "/v1/changes/app-add", payload: payload() }, {
      onSuccess: (r: any) => { onResult(r.pr?.url || "PR d'ajout créée", true); onClose(); },
      onError: (e: any) => { onResult(e.message, false); setBusy(false); },
    });
  };

  const fld = (k: string, label: string, ph?: string, type?: string) =>
    <Field label={label} value={f[k] || ""} onChange={set(k)} ph={ph} type={type} />;

  return (
    <Dialog title="Nouvelle application" onClose={onClose} foot={
      <>
        <button className="btn secondary" onClick={doPreview} disabled={busy}><Icon name="eye" /> Prévisualiser</button>
        <ActionButton variant="pr" label="Créer la PR" onClick={doCreate} disabled={busy} />
      </>
    }>
      <label className="field" style={{ marginBottom: 12 }}>Type de déploiement
        <select value={mode} onChange={(e) => { setMode(e.target.value as Mode); setPreview(null); }}>
          {MODES.map((m) => <option key={m.key} value={m.key}>{m.label}</option>)}
        </select>
        <span className="faint" style={{ fontSize: 11 }}>{MODES.find((m) => m.key === mode)!.hint}</span>
      </label>

      <div className="row" style={{ marginBottom: 8 }}>
        {fld("name", "Nom", "auto si vide")}
        {fld("port", "Port", "0 = aucun", "number")}
      </div>

      {mode === "image" && (
        <div className="row" style={{ marginBottom: 8 }}>
          {fld("image", "Image", "ghcr.io/org/app")}
          {fld("tag", "Tag", "1.2.3")}
        </div>
      )}
      {mode === "compose" && (
        <>
          <label className="field" style={{ marginBottom: 8 }}>Contenu docker-compose.yml <span className="faint">(laisser vide pour tirer depuis git)</span>
            <textarea value={f.compose || ""} onChange={set("compose")} placeholder="services:&#10;  app:&#10;    image: ..." style={{ minHeight: 120 }} />
          </label>
          <div className="row" style={{ marginBottom: 4 }}>
            {fld("repo", "Repo git (source)", "https://github.com/org/repo")}
            {fld("rev", "Rev / tag git", "main")}
          </div>
          <div className="row" style={{ marginBottom: 8 }}>
            {fld("dir", "Chemin du compose dans le repo", "ex. deploy/ (vide = racine)")}
          </div>
          <p className="faint" style={{ fontSize: 11, marginTop: 0 }}>Compose en ligne OU repo+rev (+chemin). Le repo cible le docker-compose ; sans chemin, racine du repo.</p>
        </>
      )}
      {(mode === "process" || mode === "dockerfile") && (
        <div className="row" style={{ marginBottom: 8 }}>
          {fld("repo", "Repo git", "https://github.com/org/repo")}
          {fld("rev", "Rev / branche", "main")}
        </div>
      )}
      {mode === "process" && (
        <>
          <div className="row" style={{ marginBottom: 8 }}>
            {fld("runtime", "Runtime", "nodejs / python3 / go")}
            {fld("packages", "Paquets Nix (espace)", "ffmpeg git")}
          </div>
          <div className="row" style={{ marginBottom: 8 }}>
            {fld("build_cmd", "Build", "npm ci && npm run build")}
            {fld("start_cmd", "Start", "npm start")}
          </div>
        </>
      )}
      {mode === "dockerfile" && (
        <div className="row" style={{ marginBottom: 8 }}>{fld("dir", "Répertoire (Dockerfile)", "optionnel")}</div>
      )}

      <label className="field" style={{ marginBottom: 4 }}>Raison
        <input value={f.reason || ""} onChange={set("reason")} placeholder="pourquoi cette app" />
      </label>

      {preview && (
        <div style={{ marginTop: 14 }}>
          <AlertBanner tone={preview.risk === "high" ? "bad" : preview.risk === "medium" ? "warn" : "info"} title={`Prévisualisation${preview.risk ? ` · risque ${preview.risk}` : ""}`}>
            {preview.summary}
          </AlertBanner>
          {(preview.warnings || []).length ? (
            <div className="row" style={{ marginBottom: 8 }}>{preview.warnings!.map((w, i) => <span key={i} className="badge risk nodot">{w}</span>)}</div>
          ) : null}
          {(preview.checks || []).length ? (
            <div className="row" style={{ marginBottom: 8 }}>{preview.checks!.map((c, i) => <span key={i} className="badge runtime-ok">{c}</span>)}</div>
          ) : null}
          {(preview.files || []).length ? (
            <div className="muted" style={{ fontSize: 12 }}>
              Fichiers générés : {preview.files!.map((x) => <span key={x.path} className="chip">{x.path}</span>)}
            </div>
          ) : null}
        </div>
      )}
    </Dialog>
  );
}
