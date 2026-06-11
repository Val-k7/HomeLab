import { useEffect, useState } from "react";
import { apiGet, apiPost } from "../api/client";
import { ActionButton, Dialog, Icon, AlertBanner } from "../components";

// Structured catalog add. The server (/v1/changes/catalog-add) validates every
// field (allowlist regexes, nixString escaping) and generates + splices the
// Nix entry into config/catalogs.nix itself — the client never submits
// generated Nix. The file is fetched here only to warn early when the
// catalogs block is missing; the preview below mirrors the server output.
const PATH = "config/catalogs.nix";
const TRUST = ["official", "community", "untrusted"];
const POLICY = ["strict", "warn"];
const CATEGORY = ["", "media", "network", "dev", "data", "monitoring", "misc"];
const reId = /^[a-z0-9][a-z0-9-]{0,39}$/;
const reRepo = /^https:\/\/[^\s"'\\$]{1,280}$/;
const reRef = /^[A-Za-z0-9._/-]{1,100}$/;
const BLOCK = /catalogs = \[\n/;
const MAX: Record<string, number> = { id: 40, name: 80, description: 200, repo: 290, ref: 100 };
// Mirror of control-api nixString (escapes \ " $) so the preview matches what
// the server generates and stays inert Nix.
const nixStr = (s: string) => `"${s.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\$/g, "\\$")}"`;

export function AddCatalogDialog({ existing, onClose, onResult, initial }: {
  existing: string[]; onClose: () => void; onResult: (t: string, ok: boolean) => void;
  // When set, the dialog edits this existing catalog (id locked, PR posts to
  // /v1/changes/catalog-update) instead of adding a new one.
  initial?: Record<string, string>;
}) {
  const editing = !!initial;
  const [base, setBase] = useState<string | null>(null);
  const [loadErr, setLoadErr] = useState("");
  const [v, setV] = useState<Record<string, string>>(
    initial
      ? { trust: "community", policy: "strict", category: "", ...initial }
      : { trust: "community", policy: "strict", category: "" },
  );
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const set = (k: string) => (e: any) => setV((s) => ({ ...s, [k]: e.target.value }));

  useEffect(() => {
    apiGet(`/v1/configfile?path=${encodeURIComponent(PATH)}`)
      .then((r: any) => setBase(r.content || ""))
      .catch((e: any) => setLoadErr(e.message));
  }, []);

  const dup = !editing && existing.includes((v.id || "").trim());
  const valid = reId.test(v.id || "") && reRepo.test(v.repo || "") && reRef.test((v.ref || "").trim()) && !dup;
  const ready = valid && base != null && BLOCK.test(base);

  const buildEntry = () => {
    const lines = [
      `      id = ${nixStr(v.id || "")};`,
      `      repo = ${nixStr(v.repo || "")};`,
      `      ref = ${nixStr((v.ref || "").trim())};`,
      `      trust = ${nixStr(v.trust || "community")};`,
      `      policy = ${nixStr(v.policy || "strict")};`,
    ];
    if (v.name?.trim()) lines.push(`      name = ${nixStr(v.name.trim())};`);
    if (v.description?.trim()) lines.push(`      description = ${nixStr(v.description.trim())};`);
    if (v.category) lines.push(`      category = ${nixStr(v.category)};`);
    return "    {\n" + lines.join("\n") + "\n    }";
  };
  const entry = buildEntry();

  const submit = () => {
    if (!ready) return;
    setBusy(true);
    apiPost(editing ? "/v1/changes/catalog-update" : "/v1/changes/catalog-add", {
      id: v.id, repo: v.repo, ref: (v.ref || "").trim(),
      trust: v.trust || "community", policy: v.policy || "strict",
      name: v.name?.trim() || "", description: v.description?.trim() || "", category: v.category || "",
      reason: `${editing ? "update" : "add"} catalog ${v.id}`,
    })
      .then((r: any) => { onResult(r.pr?.url || "PR catalogue créée", true); onClose(); })
      // Show the error INSIDE the dialog — a toast behind the overlay is unreadable.
      .catch((e: any) => { setErr(e.message); setBusy(false); });
  };

  const Fld = (k: string, label: string, ph?: string, lock?: boolean) => (
    <label className="field" style={{ flex: 1, minWidth: 160 }}>{label}
      <input value={v[k] || ""} onChange={set(k)} placeholder={ph} maxLength={MAX[k]} disabled={lock} />
    </label>
  );

  return (
    <Dialog title={editing ? `Modifier le catalogue · ${initial?.id}` : "Ajouter un catalogue workshop"} onClose={onClose}
      foot={<ActionButton variant="pr" label="Créer la PR" onClick={submit} disabled={!ready || busy} />}>
      {loadErr ? <AlertBanner tone="bad" title="Lecture de catalogs.nix impossible">{loadErr}</AlertBanner> : null}
      {base != null && !BLOCK.test(base) ? (
        <AlertBanner tone="warn" title="Bloc catalogs introuvable">Format non standard — éditez <span className="mono">config/catalogs.nix</span> directement.</AlertBanner>
      ) : null}

      <div className="row" style={{ marginBottom: 8 }}>
        {Fld("id", "ID", "homelab-official", editing)}
        {Fld("name", "Nom (optionnel)", "HomeLab Official")}
      </div>
      <div className="row" style={{ marginBottom: 8 }}>
        {Fld("repo", "Repo git (https)", "https://github.com/org/catalog")}
        {Fld("ref", "Ref (tag/SHA, jamais branche)", "v1.0.0")}
      </div>
      <div className="row" style={{ marginBottom: 8 }}>
        <label className="field">Confiance<select value={v.trust} onChange={set("trust")}>{TRUST.map((t) => <option key={t}>{t}</option>)}</select></label>
        <label className="field">Policy<select value={v.policy} onChange={set("policy")}>{POLICY.map((p) => <option key={p}>{p}</option>)}</select></label>
        <label className="field">Catégorie<select value={v.category} onChange={set("category")}>{CATEGORY.map((c) => <option key={c} value={c}>{c || "—"}</option>)}</select></label>
      </div>
      <label className="field" style={{ marginBottom: 10 }}>Description (optionnel)<input value={v.description || ""} onChange={set("description")} placeholder="Apps pinnées par digest." maxLength={MAX.description} /></label>
      {dup ? <p className="msg bad">Un catalogue « {v.id} » existe déjà.</p> : null}
      {err ? <p className="msg bad">{err}</p> : null}

      <div className="muted" style={{ fontSize: 12, marginBottom: 4, display: "flex", alignItems: "center", gap: 6 }}><Icon name="library" /> Entrée générée :</div>
      <pre>{entry}</pre>
    </Dialog>
  );
}
