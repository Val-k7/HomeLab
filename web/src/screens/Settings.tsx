import { useEffect, useState } from "react";
import { ActionButton, useMsg, Tabs, SectionHead, AlertBanner, Icon, useConfirm } from "../components";
import { apiGet, apiPost } from "../api/client";
import { useLibrary } from "../api/hooks";
import { AddCatalogDialog } from "./AddCatalogDialog";

const PLATFORM = "config/platform.nix";

// A string is safe to splice back into a quoted Nix value: no quote/escape/
// antiquotation characters, single line. Mirrors the server's validators.
const nixSafe = (s: string) => !/["\\$\n\r]/.test(s);

// Structured editor for platform.nix: every commonly-touched scalar edited as
// a form control, regex-replaced in place so comments and layout survive.
// Storage classes are managed on the Storage screen; observability detail and
// network stay in the advanced raw editor.
function PlatformConfig() {
  const [base, setBase] = useState<string | null>(null);
  const [v, setV] = useState<Record<string, string>>({});
  const [obs, setObs] = useState(false);
  const [classes, setClasses] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const { setMsg, node } = useMsg();
  const set = (k: string) => (e: any) => setV((s) => ({ ...s, [k]: e.target.value }));
  const get = (c: string, re: RegExp) => (c.match(re) || [, ""])[1];

  useEffect(() => {
    apiGet(`/v1/configfile?path=${encodeURIComponent(PLATFORM)}`)
      .then((r: any) => {
        const c = r.content || ""; setBase(c);
        setV({
          hostname: get(c, /hostname = "([^"]*)"/), timezone: get(c, /timezone = "([^"]*)"/), locale: get(c, /locale = "([^"]*)"/),
          backend: get(c, /backend = "([^"]*)"/), repository: get(c, /repository = "([^"]*)"/), schedule: get(c, /schedule = "([^"]*)"/),
          retDaily: get(c, /retention = \{ daily = (\d+);/), retWeekly: get(c, /weekly = (\d+);/), retMonthly: get(c, /monthly = (\d+); \};/),
          defaultStorageClass: get(c, /defaultStorageClass = "([^"]*)"/),
          updatePolicyDefault: get(c, /updatePolicyDefault = "([^"]*)"/),
          defaultVisibility: get(c, /defaultVisibility = "([^"]*)"/),
          dataRoot: get(c, /dataRoot = "([^"]*)"/), secretsRoot: get(c, /secretsRoot = "([^"]*)"/),
        });
        setObs(get(c, /observability = \{\s*\n\s*enable = (true|false);/) === "true");
        const body = (c.match(/storageClasses = \{([\s\S]*?)\n  \};/) || [, ""])[1];
        setClasses([...body.matchAll(/^\s{4}([a-zA-Z_][a-zA-Z0-9_-]*) = \{/gm)].map((m) => m[1]));
      })
      .catch((e: any) => setMsg({ text: friendly(e.message), ok: false }));
  }, []);

  const badText = ["hostname", "timezone", "locale", "repository", "dataRoot", "secretsRoot"].find((k) => !nixSafe(v[k] || ""));
  const badRet = ["retDaily", "retWeekly", "retMonthly"].find((k) => !/^\d{1,4}$/.test(v[k] || ""));

  const save = () => {
    if (base == null || badText || badRet) return;
    const next = base
      .replace(/hostname = "[^"]*"/, `hostname = "${v.hostname}"`)
      .replace(/timezone = "[^"]*"/, `timezone = "${v.timezone}"`)
      .replace(/locale = "[^"]*"/, `locale = "${v.locale}"`)
      .replace(/backend = "[^"]*"/, `backend = "${v.backend}"`)
      .replace(/repository = "[^"]*"/, `repository = "${v.repository}"`)
      .replace(/schedule = "[^"]*"/, `schedule = "${v.schedule}"`)
      .replace(/retention = \{ daily = \d+; weekly = \d+; monthly = \d+; \};/, `retention = { daily = ${v.retDaily}; weekly = ${v.retWeekly}; monthly = ${v.retMonthly}; };`)
      .replace(/defaultStorageClass = "[^"]*"/, `defaultStorageClass = "${v.defaultStorageClass}"`)
      .replace(/updatePolicyDefault = "[^"]*"/, `updatePolicyDefault = "${v.updatePolicyDefault}"`)
      .replace(/defaultVisibility = "[^"]*"/, `defaultVisibility = "${v.defaultVisibility}"`)
      .replace(/dataRoot = "[^"]*"/, `dataRoot = "${v.dataRoot}"`)
      .replace(/secretsRoot = "[^"]*"/, `secretsRoot = "${v.secretsRoot}"`)
      .replace(/(observability = \{\s*\n\s*enable = )(true|false)/, `$1${obs}`);
    setBusy(true);
    apiPost("/v1/changes/platform-config", { content: next, reason: "edit platform config via structured editor" })
      .then((r: any) => setMsg({ text: r.pr?.url || "PR créée", ok: true }))
      .catch((e: any) => setMsg({ text: friendly(e.message), ok: false }))
      .finally(() => setBusy(false));
  };

  const Txt = (k: string, label: string, ph?: string) => (
    <label className="field" style={{ flex: 1, minWidth: 170 }}><span>{label}</span>
      <input value={v[k] || ""} onChange={set(k)} placeholder={ph} disabled={base == null} />
    </label>
  );
  const Sel = (k: string, label: string, opts: string[]) => (
    <label className="field"><span>{label}</span>
      <select value={v[k] || ""} onChange={set(k)} disabled={base == null}>
        {opts.map((o) => <option key={o} value={o}>{o}</option>)}
      </select>
    </label>
  );

  return (
    <div className="card pad-lg" style={{ marginBottom: 16 }}>
      <h3 style={{ marginTop: 0 }}><Icon name="server" /> Plateforme <span className="mono muted">— platform.nix (structuré)</span></h3>
      {node}
      <div className="row" style={{ marginBottom: 8, flexWrap: "wrap" }}>
        {Txt("hostname", "Hostname")}{Txt("timezone", "Timezone", "Europe/Paris")}{Txt("locale", "Locale", "en_US.UTF-8")}
      </div>
      <div className="row" style={{ marginBottom: 8, flexWrap: "wrap" }}>
        {Sel("defaultStorageClass", "Classe de stockage par défaut", classes.length ? classes : [v.defaultStorageClass || ""])}
        {Sel("updatePolicyDefault", "Politique de màj par défaut", ["manual", "autoLow", "critical"])}
        {Sel("defaultVisibility", "Visibilité par défaut", ["private", "public"])}
      </div>
      <div className="row" style={{ marginBottom: 8, flexWrap: "wrap" }}>
        {Txt("dataRoot", "dataRoot")}{Txt("secretsRoot", "secretsRoot")}
      </div>
      <h4 style={{ margin: "14px 0 8px" }}><Icon name="backups" /> Sauvegarde</h4>
      <div className="row" style={{ marginBottom: 8, flexWrap: "wrap" }}>
        {Sel("backend", "Backend", ["restic", "borg", "rsync"])}
        {Sel("schedule", "Planification", ["hourly", "daily", "weekly", "monthly"])}
        {Txt("repository", "Dépôt (restic)", "s3:… / /mnt/backup / rest:https://…")}
      </div>
      <div className="row" style={{ marginBottom: 8 }}>
        {(["retDaily", "retWeekly", "retMonthly"] as const).map((k, i) => (
          <label key={k} className="field" style={{ width: 140 }}><span>Rétention {["daily", "weekly", "monthly"][i]}</span>
            <input value={v[k] || ""} onChange={set(k)} inputMode="numeric" disabled={base == null} />
          </label>
        ))}
      </div>
      <label className="row" style={{ gap: 6, fontSize: 13, marginBottom: 12 }}>
        <input type="checkbox" checked={obs} onChange={(e) => setObs(e.target.checked)} disabled={base == null} /> Observabilité (node_exporter + Prometheus)
      </label>
      {badText ? <p className="msg bad">Caractère interdit dans « {badText} » (pas de " \ $)</p> : null}
      {badRet ? <p className="msg bad">Rétention invalide ({badRet}) : nombre attendu</p> : null}
      <ActionButton variant="pr" label="Créer PR" onClick={save} disabled={base == null || busy || !!badText || !!badRet} />
      <p className="muted gap-top">Les classes de stockage se gèrent dans <b>Storage</b>. Le secret du dépôt backup via Secrets/SOPS.</p>
    </div>
  );
}

const POLICIES = "config/policies.nix";
const TIERS = ["low", "medium", "high", "critical"] as const;
const UPDATE_POLICIES = ["manual", "autoLow", "critical"];

// Structured editor for policies.nix: regex-replaces the scalar/list values in
// place (comments and layout untouched), exactly like BackupConfig does for
// platform.nix. The forbidden/knownPermissions blocks stay raw-editor-only.
function PoliciesConfig() {
  const [base, setBase] = useState<string | null>(null);
  const [b, setB] = useState<Record<string, boolean>>({});
  const [registries, setRegistries] = useState("");
  const [automerge, setAutomerge] = useState<string[]>([]);
  const [tiers, setTiers] = useState<Record<string, { required: boolean; restoreTest: boolean }>>({});
  const [reserved, setReserved] = useState("");
  const [perms, setPerms] = useState<string[]>([]);
  const [newPerm, setNewPerm] = useState("");
  const [busy, setBusy] = useState(false);
  const { setMsg, node } = useMsg();

  const flag = (c: string, re: RegExp) => (c.match(re) || [])[1] === "true";

  useEffect(() => {
    apiGet(`/v1/configfile?path=${encodeURIComponent(POLICIES)}`)
      .then((r: any) => {
        const c = r.content || ""; setBase(c);
        setB({
          strict: flag(c, /\n  strict = (true|false);/),
          requireDigest: flag(c, /requireDigest = (true|false);/),
          allowLatest: flag(c, /allowLatest = (true|false);/),
          allowPublic: flag(c, /allowPublic = (true|false);/),
          databaseBlocksAutomerge: flag(c, /databaseBlocksAutomerge = (true|false);/),
          allowInline: flag(c, /allowInline = (true|false);/),
          privileged: flag(c, /privileged = (true|false);/),
          hostRootMount: flag(c, /hostRootMount = (true|false);/),
          dockerSocket: flag(c, /dockerSocket = (true|false);/),
          secretInline: flag(c, /secretInline = (true|false);/),
        });
        setReserved(((c.match(/reserved = \[([^\]]*)\]/) || [, ""])[1].match(/\d+/g) || []).join(" "));
        setPerms(((c.match(/knownPermissions = \[([^\]]*)\]/s) || [, ""])[1].match(/"([^"]+)"/g) || []).map((s: string) => s.slice(1, -1)));
        setRegistries(((c.match(/allowedRegistries = \[([^\]]*)\]/) || [, ""])[1].match(/"([^"]+)"/g) || []).map((s: string) => s.slice(1, -1)).join(" "));
        setAutomerge(((c.match(/automergeAllowed = \[([^\]]*)\]/) || [, ""])[1].match(/"([^"]+)"/g) || []).map((s: string) => s.slice(1, -1)));
        const t: Record<string, { required: boolean; restoreTest: boolean }> = {};
        for (const tier of TIERS) {
          const m = c.match(new RegExp(`${tier} = \\{ required = (true|false); restoreTest = (true|false); \\};`));
          t[tier] = { required: m?.[1] === "true", restoreTest: m?.[2] === "true" };
        }
        setTiers(t);
      })
      .catch((e: any) => setMsg({ text: friendly(e.message), ok: false }));
  }, []);

  const badReg = registries.split(/[\s,]+/).filter(Boolean).find((r) => !/^[a-z0-9.:-]+$/.test(r));
  const badPort = reserved.split(/[\s,]+/).filter(Boolean).find((p) => !/^\d{1,5}$/.test(p));
  const rePerm = /^[a-z0-9][a-z0-9-]{0,40}$/;

  const save = () => {
    if (base == null || badReg || badPort) return;
    const regList = registries.split(/[\s,]+/).filter(Boolean);
    const portList = reserved.split(/[\s,]+/).filter(Boolean);
    let next = base
      .replace(/\n  strict = (true|false);/, `\n  strict = ${b.strict};`)
      .replace(/requireDigest = (true|false);/, `requireDigest = ${b.requireDigest};`)
      .replace(/allowLatest = (true|false);/, `allowLatest = ${b.allowLatest};`)
      .replace(/allowPublic = (true|false);/, `allowPublic = ${b.allowPublic};`)
      .replace(/databaseBlocksAutomerge = (true|false);/, `databaseBlocksAutomerge = ${b.databaseBlocksAutomerge};`)
      .replace(/allowInline = (true|false);/, `allowInline = ${b.allowInline};`)
      .replace(/privileged = (true|false);/, `privileged = ${b.privileged};`)
      .replace(/hostRootMount = (true|false);/, `hostRootMount = ${b.hostRootMount};`)
      .replace(/dockerSocket = (true|false);/, `dockerSocket = ${b.dockerSocket};`)
      .replace(/secretInline = (true|false);/, `secretInline = ${b.secretInline};`)
      .replace(/reserved = \[[^\]]*\]/, `reserved = [ ${portList.join(" ")} ]`)
      .replace(/knownPermissions = \[[^\]]*\]/s, `knownPermissions = [\n${perms.map((p) => `    "${p}"`).join("\n")}\n  ]`)
      .replace(/allowedRegistries = \[[^\]]*\]/, `allowedRegistries = [ ${regList.map((r) => `"${r}"`).join(" ")} ]`)
      .replace(/automergeAllowed = \[[^\]]*\]/, `automergeAllowed = [ ${automerge.map((p) => `"${p}"`).join(" ")} ]`);
    for (const tier of TIERS) {
      const t = tiers[tier];
      if (!t) continue;
      next = next.replace(
        new RegExp(`${tier} = \\{ required = (true|false); restoreTest = (true|false); \\};`),
        `${tier} = { required = ${t.required}; restoreTest = ${t.restoreTest}; };`,
      );
    }
    setBusy(true);
    apiPost("/v1/changes/policy-config", { content: next, reason: "edit policies via structured editor" })
      .then((r: any) => setMsg({ text: r.pr?.url || "PR créée", ok: true }))
      .catch((e: any) => setMsg({ text: friendly(e.message), ok: false }))
      .finally(() => setBusy(false));
  };

  const Check = (k: string, label: string) => (
    <label className="row" style={{ gap: 6, fontSize: 13 }}>
      <input type="checkbox" checked={!!b[k]} onChange={(e) => setB((s) => ({ ...s, [k]: e.target.checked }))} disabled={base == null} /> {label}
    </label>
  );

  return (
    <div className="card pad-lg" style={{ marginBottom: 16 }}>
      <h3 style={{ marginTop: 0 }}><Icon name="security" /> Policies <span className="mono muted">— policies.nix (structuré)</span></h3>
      {node}
      <div className="row" style={{ gap: 18, flexWrap: "wrap", marginBottom: 12 }}>
        {Check("strict", "Mode strict (lockdown)")}
        {Check("requireDigest", "Digest obligatoire (images)")}
        {Check("allowLatest", "Autoriser les tags mouvants (latest…)")}
        {Check("allowPublic", "Autoriser les ports publics")}
        {Check("databaseBlocksAutomerge", "DB bloque l'automerge")}
        {Check("allowInline", "Secrets inline autorisés")}
      </div>
      <div className="row" style={{ gap: 18, flexWrap: "wrap", marginBottom: 12 }}>
        <span className="muted" style={{ fontSize: 12 }}>Capacités interdites sans permission :</span>
        {Check("privileged", "privileged-container")}
        {Check("hostRootMount", "host-root-mount")}
        {Check("dockerSocket", "docker-socket")}
        {Check("secretInline", "secret inline")}
      </div>
      <label className="field" style={{ marginBottom: 10 }}>
        <span>Ports réservés (séparés par des espaces)</span>
        <input value={reserved} onChange={(e) => setReserved(e.target.value)} disabled={base == null} />
      </label>
      {badPort ? <p className="msg bad">Port invalide : {badPort}</p> : null}
      <div className="field" style={{ marginBottom: 12 }}>
        <span>Permissions connues (apps)</span>
        <div className="row" style={{ flexWrap: "wrap", gap: 6, marginTop: 6 }}>
          {perms.map((p) => (
            <span key={p} className="chip" style={{ display: "inline-flex", gap: 5, alignItems: "center" }}>
              {p}
              <button className="chip-x" title="retirer" onClick={() => setPerms((s) => s.filter((x) => x !== p))} disabled={base == null}>×</button>
            </span>
          ))}
          <input value={newPerm} onChange={(e) => setNewPerm(e.target.value)} placeholder="nouvelle-permission"
            style={{ width: 170 }} disabled={base == null}
            onKeyDown={(e) => {
              if (e.key === "Enter" && rePerm.test(newPerm) && !perms.includes(newPerm)) { setPerms((s) => [...s, newPerm]); setNewPerm(""); }
            }} />
          <button className="btn secondary sm" disabled={base == null || !rePerm.test(newPerm) || perms.includes(newPerm)}
            onClick={() => { setPerms((s) => [...s, newPerm]); setNewPerm(""); }}>Ajouter</button>
        </div>
      </div>
      <label className="field" style={{ marginBottom: 10 }}>
        <span>Registres autorisés (séparés par des espaces; vide = check désactivé)</span>
        <input value={registries} onChange={(e) => setRegistries(e.target.value)} placeholder="docker.io ghcr.io quay.io" disabled={base == null} />
      </label>
      {badReg ? <p className="msg bad">Registre invalide : {badReg}</p> : null}
      <div className="row" style={{ gap: 14, marginBottom: 12, flexWrap: "wrap" }}>
        <span className="muted" style={{ fontSize: 12 }}>Automerge autorisé :</span>
        {UPDATE_POLICIES.map((p) => (
          <label key={p} className="row" style={{ gap: 5, fontSize: 13 }}>
            <input type="checkbox" checked={automerge.includes(p)}
              onChange={(e) => setAutomerge((s) => e.target.checked ? [...s, p] : s.filter((x) => x !== p))} disabled={base == null} />
            <span className="mono">{p}</span>
          </label>
        ))}
      </div>
      <div className="table-wrap" style={{ marginBottom: 12 }}>
        <table className="help-table">
          <thead><tr><th>Criticité</th><th>Backup requis</th><th>Restore-test requis</th></tr></thead>
          <tbody>
            {TIERS.map((tier) => (
              <tr key={tier}>
                <td className="mono">{tier}</td>
                <td><input type="checkbox" checked={!!tiers[tier]?.required}
                  onChange={(e) => setTiers((s) => ({ ...s, [tier]: { ...s[tier], required: e.target.checked } }))} disabled={base == null} /></td>
                <td><input type="checkbox" checked={!!tiers[tier]?.restoreTest}
                  onChange={(e) => setTiers((s) => ({ ...s, [tier]: { ...s[tier], restoreTest: e.target.checked } }))} disabled={base == null} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <ActionButton variant="pr" label="Créer PR" onClick={save} disabled={base == null || busy || !!badReg || !!badPort} />
      <p className="muted gap-top">Toutes les valeurs sont réécrites en place — commentaires du fichier conservés dans la PR.</p>
    </div>
  );
}

const FILES: { path: string; endpoint: string; label: string; tab: string }[] = [
  { path: "config/platform.nix", endpoint: "/v1/changes/platform-config", label: "Platform (storage, backup, defaults)", tab: "config" },
  { path: "config/policies.nix", endpoint: "/v1/changes/policy-config", label: "Policies (sécurité, update, backup)", tab: "config" },
  { path: "config/catalogs.nix", endpoint: "/v1/changes/catalog-add", label: "Catalogues workshop", tab: "catalogs" },
];

// Turn opaque control-api errors into something an operator can act on. The
// CSRF guard in particular surfaces as a cryptic 403; explain it plainly.
function friendly(msg: string): string {
  const m = msg.toLowerCase();
  if (m.includes("csrf")) return "Session expirée ou onglet resté ouvert trop longtemps. Rechargez la page puis réessayez.";
  if (m.includes("403") || m.includes("forbidden")) return "Action refusée : votre rôle ne permet pas ce changement (admin requis).";
  if (m.includes("401")) return "Session expirée. Reconnectez-vous.";
  return msg;
}

// Structured access manager: grant/revoke a GitHub identity's role.
function AccessRoles() {
  const [user, setUser] = useState("");
  const [role, setRole] = useState("operator");
  const { setMsg, node } = useMsg();

  const send = (remove: boolean) => {
    if (!user) return;
    apiPost("/v1/changes/access-role", { user, role, remove, reason: remove ? `revoke ${user}` : `grant ${user} ${role}` })
      .then((r: any) => setMsg({ text: r.pr?.url || "PR créée", ok: true }))
      .catch((e: any) => setMsg({ text: friendly(e.message), ok: false }));
  };

  return (
    <div className="card pad-lg">
      {node}
      <div className="row">
        <label className="field" style={{ flex: 1 }}>Identité (email / login GitHub)
          <input placeholder="ex. alice@example.com" value={user} onChange={(e) => setUser(e.target.value)} />
        </label>
        <label className="field">Rôle
          <select value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="viewer">viewer</option>
            <option value="operator">operator</option>
            <option value="maintainer">maintainer</option>
            <option value="admin">admin</option>
          </select>
        </label>
      </div>
      <div className="row gap-top">
        <ActionButton variant="pr" label="Accorder l'accès" icon="plus" onClick={() => send(false)} />
        <ActionButton variant="pr" label="Révoquer" onClick={() => send(true)} danger />
      </div>
      <p className="muted gap-top">L'identité correspond à ce que <span className="mono">/v1/me</span> affiche. Modifie <span className="mono">config/access.json</span> par PR. Rôle admin requis.</p>
    </div>
  );
}

// splitCommentHeader separates the leading `#` comment banner of a Nix file
// from its body, so the editor can collapse the banner without losing it: the
// PR is always header + edited body, byte-identical when nothing changes.
function splitCommentHeader(content: string): { header: string; body: string } {
  const lines = content.split("\n");
  let i = 0;
  while (i < lines.length && (lines[i].startsWith("#") || lines[i].trim() === "")) i++;
  // Only treat it as a banner when the file actually starts with comments.
  if (i === 0 || !lines[0].startsWith("#")) return { header: "", body: content };
  return { header: lines.slice(0, i).join("\n") + "\n", body: lines.slice(i).join("\n") };
}

function Editor({ path, endpoint, label }: { path: string; endpoint: string; label: string }) {
  const [header, setHeader] = useState("");
  const [body, setBody] = useState("");
  const [loaded, setLoaded] = useState(false);
  const { setMsg, node } = useMsg();
  const { confirm, node: confirmNode } = useConfirm();

  useEffect(() => {
    apiGet(`/v1/configfile?path=${encodeURIComponent(path)}`)
      .then((r: any) => {
        const { header, body } = splitCommentHeader(r.content || "");
        setHeader(header); setBody(body); setLoaded(true);
      })
      .catch((e: any) => { setMsg({ text: friendly(e.message), ok: false }); setLoaded(true); });
  }, [path]);

  const submit = async () => {
    const r = await confirm(`Créer une PR pour ${path} ?`, { reason: true, reasonLabel: "Raison du changement", confirmLabel: "Créer PR" });
    if (!r.ok) return;
    apiPost(endpoint, { content: header + body, reason: r.reason || "edit via settings" })
      .then((x: any) => setMsg({ text: x.pr?.url || "PR créée", ok: true }))
      .catch((e: any) => setMsg({ text: friendly(e.message), ok: false }));
  };

  return (
    <div className="card pad-lg" style={{ marginBottom: 16 }}>
      <div className="section-head" style={{ margin: "0 0 10px" }}>
        <h3 style={{ margin: 0 }}>{label}</h3>
        <span className="mono muted">{path}</span>
      </div>
      {node}
      {confirmNode}
      {header ? (
        <details className="comment-fold">
          <summary><Icon name="chevron" /> Commentaires du fichier ({header.trim().split("\n").length} lignes — repliés, conservés dans la PR)</summary>
          <pre>{header}</pre>
        </details>
      ) : null}
      <textarea value={body} onChange={(e) => setBody(e.target.value)} disabled={!loaded} />
      <div className="row gap-top">
        <ActionButton variant="pr" label="Créer PR" onClick={submit} />
        <span className="muted">Aucune écriture directe — tout passe par une PR. Rôle admin requis.</span>
      </div>
    </div>
  );
}

// Catalogs tab: fully structured — current catalogs in a designed table with
// edit/remove (PR), plus the structured add dialog. No raw Nix here; the raw
// catalogs.nix editor lives in the advanced fold of the Configuration tab.
function CatalogsManager() {
  const lib = useLibrary();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<any | null>(null);
  const { setMsg, node } = useMsg();
  const { confirm, node: confirmNode } = useConfirm();
  const catalogs: any[] = (lib.data as any)?.catalogs || [];
  const ids = catalogs.map((c: any) => c.id);

  const remove = async (c: any) => {
    if (!(await confirm(`Retirer le catalogue « ${c.id} » ? Une PR sera créée; les modules installés restent en place.`, { danger: true, confirmLabel: "Retirer" })).ok) return;
    apiPost("/v1/changes/catalog-remove", { id: c.id, reason: `remove catalog ${c.id}` })
      .then((r: any) => setMsg({ text: r.pr?.url || "PR de retrait créée", ok: true }))
      .catch((e: any) => setMsg({ text: friendly(e.message), ok: false }));
  };

  return (
    <>
      <div className="section-head" style={{ marginTop: 0 }}>
        <h3 style={{ margin: 0 }}>Catalogues workshop <span className="count">· {catalogs.length}</span></h3>
        <button className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" /> Ajouter un catalogue</button>
      </div>
      {node}
      {confirmNode}

      {catalogs.length ? (
        <div className="table-wrap" style={{ marginBottom: 14 }}>
          <table>
            <thead><tr><th>Nom</th><th>Dépôt</th><th>Réf</th><th>Confiance</th><th>Policy</th><th style={{ textAlign: "right" }}></th></tr></thead>
            <tbody>
              {catalogs.map((c: any) => (
                <tr key={c.id}>
                  <td><div className="cell-stack"><b>{c.name || c.id}</b>{c.description ? <span className="muted" style={{ fontSize: 12 }}>{c.description}</span> : null}</div></td>
                  <td className="mono">{c.repo}</td>
                  <td className="mono">{c.ref}</td>
                  <td><span className="chip">{c.trust}</span></td>
                  <td><span className="chip">{c.policy || "strict"}</span></td>
                  <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                    <button className="btn secondary sm" onClick={() => setEditing(c)}><Icon name="settings" /> Modifier</button>
                    <button className="btn danger sm" onClick={() => remove(c)}>Retirer</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="muted">Aucun catalogue. Ajoutez-en un — il apparaîtra ici et dans la Bibliothèque.</p>
      )}

      <details className="comment-fold" style={{ marginBottom: 12 }}>
        <summary><Icon name="library" /> Aide — champs d'un catalogue</summary>
        <table className="help-table">
          <tbody>
            <tr><td className="mono">id</td><td>requis</td><td>identifiant unique, minuscules/chiffres/tirets</td></tr>
            <tr><td className="mono">repo</td><td>requis</td><td>URL git https du dépôt du catalogue</td></tr>
            <tr><td className="mono">ref</td><td>requis</td><td>tag ou SHA de commit (40 car.) — jamais une branche mouvante</td></tr>
            <tr><td className="mono">trust</td><td>requis</td><td>official · community · untrusted</td></tr>
            <tr><td className="mono">policy</td><td>optionnel</td><td>strict (défaut) ou warn</td></tr>
            <tr><td className="mono">name / description / category</td><td>optionnels</td><td>affichage UI (catégories: media, network, dev, data, monitoring, misc)</td></tr>
          </tbody>
        </table>
      </details>

      <details className="comment-fold" style={{ marginBottom: 12 }}>
        <summary><Icon name="plus" /> Aide — écrire un module (catalog.json du dépôt)</summary>
        <p className="muted" style={{ margin: "8px 0" }}>
          Le dépôt du catalogue expose un <span className="mono">catalog.json</span> à la racine avec une liste <span className="mono">modules</span>.
          Chaque module: <span className="mono">id, name, version, repo, sha</span> (commit 40 car.), un <span className="mono">runner</span> (image · process · dockerfile · compose) et ses champs.
          Pour <span className="mono">runner=image</span>, pinnez toujours <span className="mono">digest</span> (sha256:…) sinon la CI stricte rejette l'install.
        </p>
        <pre>{`{
  "modules": [
    {
      "id": "mon-app", "name": "Mon App", "version": "1.0.0",
      "repo": "https://github.com/org/catalog", "sha": "<commit 40 hex>",
      "runner": "image",
      "image": "org/app", "tag": "v1.2.3",
      "digest": "sha256:<64 hex>",
      "port": 8080, "permissions": ["tailnet-port"]
    }
  ]
}`}</pre>
      </details>

      {adding && <AddCatalogDialog existing={ids} onClose={() => setAdding(false)} onResult={(t, ok) => setMsg({ text: t, ok })} />}
      {editing && (
        <AddCatalogDialog
          existing={ids}
          initial={{
            id: editing.id, repo: editing.repo || "", ref: editing.ref || "",
            trust: editing.trust || "community", policy: editing.policy || "strict",
            name: editing.name || "", description: editing.description || "", category: editing.category || "",
          }}
          onClose={() => setEditing(null)}
          onResult={(t, ok) => setMsg({ text: t, ok })}
        />
      )}
    </>
  );
}

export function Settings() {
  const [tab, setTab] = useState("access");
  return (
    <div>
      <AlertBanner tone="info" title="Édition par PR uniquement">
        Chaque changement de configuration crée une pull request — jamais d'écriture directe sur l'infra. Rôle admin requis côté serveur.
      </AlertBanner>

      <Tabs
        active={tab}
        onChange={setTab}
        tabs={[
          { key: "access", label: "Accès & rôles", icon: "security" },
          { key: "config", label: "Configuration", icon: "settings" },
          { key: "catalogs", label: "Catalogues", icon: "library" },
        ]}
      />

      {tab === "access" && (<><SectionHead title="Accès & rôles" sub="qui peut atteindre le homelab et à quel niveau" /><AccessRoles /></>)}
      {tab === "config" && (
        <>
          <SectionHead title="Configuration plateforme" />
          <PlatformConfig />
          <PoliciesConfig />
          <details className="comment-fold" style={{ marginBottom: 16 }}>
            <summary><Icon name="terminal" /> Édition avancée (Nix brut) — réservé aux cas non couverts ci-dessus</summary>
            <div style={{ padding: "10px 12px" }}>
              {FILES.map((f) => <Editor key={f.path} {...f} />)}
            </div>
          </details>
        </>
      )}
      {tab === "catalogs" && <CatalogsManager />}
    </div>
  );
}
