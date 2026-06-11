import { ReactNode, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

/* Shared UI primitives for the control plane.
   StateBadge renders the canonical states; ActionButton keeps the PR (durable)
   vs runtime (audited) distinction in its label prefix. New primitives — Icon,
   MetricCard, AlertBanner, EmptyState, ActionMenu, Tabs, SectionHead — give the
   screens a consistent premium vocabulary without changing any data flow. */

/* ---------------- Icons (lean stroke set) ---------------- */
const PATHS: Record<string, ReactNode> = {
  overview: <><rect x="3" y="3" width="7" height="9" rx="1" /><rect x="14" y="3" width="7" height="5" rx="1" /><rect x="14" y="12" width="7" height="9" rx="1" /><rect x="3" y="16" width="7" height="5" rx="1" /></>,
  apps: <><path d="M21 16V8a2 2 0 0 0-1-1.7l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.7l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" /><path d="m3.3 7 8.7 5 8.7-5M12 22V12" /></>,
  library: <><path d="m12 2 9 5-9 5-9-5 9-5z" /><path d="m3 12 9 5 9-5M3 17l9 5 9-5" /></>,
  changes: <><circle cx="6" cy="6" r="2.6" /><circle cx="6" cy="18" r="2.6" /><circle cx="18" cy="9" r="2.6" /><path d="M6 8.6v6.8M8.6 6H14a3 3 0 0 1 3 3v.4" /></>,
  storage: <><ellipse cx="12" cy="5" rx="8" ry="3" /><path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6" /></>,
  secrets: <><circle cx="8" cy="15" r="4" /><path d="m10.8 12.2 8.2-8.2M16 6l2 2M19 3l2 2" /></>,
  backups: <><rect x="3" y="4" width="18" height="4" rx="1" /><path d="M5 8v11a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8M9 12h6" /></>,
  security: <><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" /><path d="m9 12 2 2 4-4" /></>,
  monitoring: <path d="M3 12h4l2 6 4-14 2 8h6" />,
  system: <><rect x="5" y="5" width="14" height="14" rx="2" /><rect x="9" y="9" width="6" height="6" rx="1" /><path d="M9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3" /></>,
  settings: <><path d="M4 6h10M18 6h2M4 12h2M10 12h10M4 18h7M15 18h5" /><circle cx="16" cy="6" r="2" /><circle cx="8" cy="12" r="2" /><circle cx="13" cy="18" r="2" /></>,
  cpu: <><rect x="6" y="6" width="12" height="12" rx="1.5" /><path d="M9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3" /></>,
  ram: <><rect x="2" y="8" width="20" height="8" rx="1.5" /><path d="M6 16v3M10 16v3M14 16v3M18 16v3M6 11v2M10 11v2M14 11v2M18 11v2" /></>,
  disk: <><circle cx="12" cy="12" r="9" /><circle cx="12" cy="12" r="2.5" /><path d="m16.5 7.5-3 3" /></>,
  server: <><rect x="3" y="4" width="18" height="7" rx="1.5" /><rect x="3" y="13" width="18" height="7" rx="1.5" /><path d="M7 7.5h.01M7 16.5h.01" /></>,
  clock: <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></>,
  check: <path d="m20 6-11 11-5-5" />,
  warn: <><path d="M10.3 3.8 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.8a2 2 0 0 0-3.4 0z" /><path d="M12 9v4M12 17h.01" /></>,
  xcircle: <><circle cx="12" cy="12" r="9" /><path d="m15 9-6 6M9 9l6 6" /></>,
  info: <><circle cx="12" cy="12" r="9" /><path d="M12 16v-4M12 8h.01" /></>,
  external: <><path d="M15 3h6v6M10 14 21 3" /><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" /></>,
  refresh: <><path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v5h-5" /></>,
  play: <path d="M6 4v16l13-8z" />,
  restart: <><path d="M3 12a9 9 0 1 0 3-6.7L3 8" /><path d="M3 3v5h5" /></>,
  terminal: <><path d="m5 8 4 4-4 4M12 16h7" /><rect x="2" y="3" width="20" height="18" rx="2" /></>,
  more: <><circle cx="5" cy="12" r="1.6" /><circle cx="12" cy="12" r="1.6" /><circle cx="19" cy="12" r="1.6" /></>,
  plus: <path d="M12 5v14M5 12h14" />,
  rollback: <><path d="M9 14 4 9l5-5" /><path d="M4 9h11a5 5 0 0 1 0 10h-3" /></>,
  heart: <path d="M19 14c1.5-1.5 3-3.4 3-5.5A4.5 4.5 0 0 0 12 5 4.5 4.5 0 0 0 2 8.5c0 2.1 1.5 4 3 5.5l7 7z" />,
  inbox: <><path d="M22 12h-6l-2 3h-4l-2-3H2" /><path d="M5.5 5.1 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.7 4H7.3a2 2 0 0 0-1.8 1.1z" /></>,
  octagon: <><path d="M7.9 2h8.2L22 7.9v8.2L16.1 22H7.9L2 16.1V7.9z" /><path d="M12 8v4M12 16h.01" /></>,
  chevron: <path d="m6 9 6 6 6-6" />,
  shield: <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />,
  history: <><path d="M3 3v6h6" /><path d="M3.5 9a9 9 0 1 0 2.1-3.4L3 9" /><path d="M12 7v5l4 2" /></>,
  download: <><path d="M12 3v12M7 10l5 5 5-5" /><path d="M5 21h14" /></>,
  power: <><path d="M12 2v10" /><path d="M18.4 6.6a9 9 0 1 1-12.8 0" /></>,
  eye: <><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z" /><circle cx="12" cy="12" r="3" /></>,
};

export function Icon({ name, className }: { name: string; className?: string }) {
  return (
    <svg className={className} width="1em" height="1em" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" style={{ flex: "none", verticalAlign: "-0.15em" }} aria-hidden>
      {PATHS[name] ?? PATHS.info}
    </svg>
  );
}

/* ---------------- Badges ---------------- */
export function StateBadge({ kind, children }: { kind: "desired" | "runtime-ok" | "runtime-bad" | "action" | "risk" | "err" | "info"; children: ReactNode }) {
  return <span className={`badge ${kind}`}>{children}</span>;
}

/* ---------------- Buttons ---------------- */
export function ActionButton({
  variant, label, onClick, disabled, danger, icon,
}: {
  variant: "pr" | "runtime";
  label: string;
  onClick: () => void;
  disabled?: boolean;
  danger?: boolean;
  icon?: string;
}) {
  const cls = danger ? "btn danger" : `btn ${variant}`;
  const prefix = variant === "pr" ? "Proposer (PR): " : "Exécuter: ";
  const ic = icon ?? (variant === "pr" ? "changes" : "play");
  return (
    <button className={cls} onClick={onClick} disabled={disabled} title={prefix + label}>
      <Icon name={ic} /> {label}
    </button>
  );
}

/* ---------------- Loading / error ---------------- */
export function Loading({ q }: { q: { isLoading: boolean; error: unknown } }) {
  if (q.isLoading) return <p className="muted">Chargement…</p>;
  if (q.error) {
    const msg = q.error instanceof Error ? q.error.message : String(q.error ?? "Erreur inconnue");
    return <p className="msg bad">Erreur : {msg || "Erreur inconnue"}</p>;
  }
  return null;
}

export function pct(v: any) {
  const n = parseFloat(v);
  return isNaN(n) ? "–" : Math.round(n) + "%";
}

/* ---------------- Metric card ---------------- */
export function MetricCard({
  label, value, sub, tone, icon, gauge, onClick,
}: {
  label: ReactNode; value: ReactNode; sub?: ReactNode;
  tone?: "ok" | "warn" | "bad" | "info" | "neutral";
  icon?: string; gauge?: number | null; onClick?: () => void;
}) {
  const t = tone && tone !== "neutral" ? tone : "";
  return (
    <div className={`card metric ${t} ${onClick ? "clickable" : ""}`} onClick={onClick}>
      <div className="m-label">{icon && <Icon name={icon} />}{label}</div>
      <div className="m-value">{value}</div>
      {sub != null && <div className="m-sub">{sub}</div>}
      {gauge != null && (
        <div className={`gauge ${t}`}><span style={{ width: `${Math.max(0, Math.min(100, gauge))}%` }} /></div>
      )}
    </div>
  );
}

/* ---------------- Alert banner ---------------- */
export function AlertBanner({
  tone, title, children,
}: { tone: "bad" | "warn" | "ok" | "info"; title: ReactNode; children?: ReactNode }) {
  const ic = tone === "bad" ? "xcircle" : tone === "warn" ? "warn" : tone === "ok" ? "check" : "info";
  return (
    <div className={`alert ${tone}`}>
      <Icon name={ic} />
      <div className="a-body">
        <div className="a-title">{title}</div>
        {children && <div className="a-text">{children}</div>}
      </div>
    </div>
  );
}

/* ---------------- Empty state ---------------- */
export function EmptyState({
  icon = "inbox", title, children, action,
}: { icon?: string; title: ReactNode; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      <Icon name={icon} className="e-icon" />
      <div className="e-title">{title}</div>
      {children && <div className="e-text">{children}</div>}
      {action}
    </div>
  );
}

/* ---------------- Section head ---------------- */
export function SectionHead({ title, sub, count, action }: { title: ReactNode; sub?: ReactNode; count?: number; action?: ReactNode }) {
  return (
    <div className="section-head">
      <h3>{title}{count != null && <span className="count">· {count}</span>}{sub && <span className="sub" style={{ textTransform: "none", letterSpacing: 0, fontWeight: 400 }}>{sub}</span>}</h3>
      {action}
    </div>
  );
}

/* ---------------- Tabs ---------------- */
export function Tabs({ tabs, active, onChange }: { tabs: { key: string; label: string; icon?: string }[]; active: string; onChange: (k: string) => void }) {
  return (
    <div className="tabs">
      {tabs.map((t) => (
        <button key={t.key} className={t.key === active ? "active" : ""} onClick={() => onChange(t.key)}>
          {t.icon && <Icon name={t.icon} />}{t.label}
        </button>
      ))}
    </div>
  );
}

/* ---------------- Action menu (⋯) ---------------- */
export function ActionMenu({ items }: { items: ({ label: string; icon?: string; danger?: boolean; onClick: () => void } | "sep" | { heading: string })[] }) {
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ top: number; right: number } | null>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  const popRef = useRef<HTMLDivElement>(null);

  // The menu is rendered in a body-level portal with fixed positioning so it is
  // never clipped by an ancestor's `overflow: hidden` (the table-wrap). Position
  // is derived from the trigger's viewport rect; we close on any scroll/resize
  // rather than chase the trigger.
  const place = () => {
    const b = btnRef.current?.getBoundingClientRect();
    if (b) setPos({ top: b.bottom + 4, right: Math.max(8, window.innerWidth - b.right) });
  };
  useEffect(() => {
    if (!open) return;
    place();
    const onDoc = (e: MouseEvent) => {
      if (!popRef.current?.contains(e.target as Node) && !btnRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    const onMove = () => setOpen(false);
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    window.addEventListener("scroll", onMove, true);
    window.addEventListener("resize", onMove);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("scroll", onMove, true);
      window.removeEventListener("resize", onMove);
    };
  }, [open]);

  return (
    <div className="menu-wrap">
      <button ref={btnRef} className="icon-btn" aria-label="Plus d'actions" title="Plus d'actions" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen((o) => !o)}><Icon name="more" /></button>
      {open && pos && createPortal(
        <div className="menu-pop" role="menu" ref={popRef} style={{ top: pos.top, right: pos.right }}>
          {items.map((it, i) => {
            if (it === "sep") return <div key={i} className="sep" />;
            if ("heading" in it) return <div key={i} className="menu-label">{it.heading}</div>;
            return (
              <button key={i} className={it.danger ? "danger" : ""} onClick={() => { setOpen(false); it.onClick(); }}>
                {it.icon && <Icon name={it.icon} />}{it.label}
              </button>
            );
          })}
        </div>,
        document.body,
      )}
    </div>
  );
}

/* ---------------- Toast-ish message ---------------- */
export function useMsg() {
  const [msg, setMsg] = useState<{ text: string; ok: boolean } | null>(null);
  const node = msg ? <div className={`msg ${msg.ok ? "ok" : "bad"}`}>{msg.text}</div> : null;
  return { setMsg, node };
}

/* ---------------- Confirm (in-app, replaces window.confirm/prompt) ----------
   Native confirm()/prompt() break after the user ticks Chrome's "prevent this
   page from creating additional dialogs": every later call silently returns
   false, so actions appear to "work once then do nothing". This hook renders an
   in-app modal instead and resolves a promise with {ok, reason}. */
type ConfirmOpts = { danger?: boolean; reason?: boolean; reasonLabel?: string; confirmLabel?: string };
type ConfirmState = ConfirmOpts & { message: string; resolve: (r: { ok: boolean; reason: string }) => void };

export function useConfirm() {
  const [st, setSt] = useState<ConfirmState | null>(null);
  const [reason, setReason] = useState("");

  const confirm = (message: string, opts: ConfirmOpts = {}) =>
    new Promise<{ ok: boolean; reason: string }>((resolve) => { setReason(""); setSt({ message, ...opts, resolve }); });

  const done = (ok: boolean) => { st?.resolve({ ok, reason }); setSt(null); };

  const node = st ? (
    <Dialog title={st.danger ? "Confirmer l'action" : "Confirmer"} onClose={() => done(false)}
      foot={<button className={st.danger ? "btn solid-danger" : "btn pr"} onClick={() => done(true)}>{st.confirmLabel || "Confirmer"}</button>}>
      <p style={{ margin: "0 0 12px" }}>{st.message}</p>
      {st.reason ? (
        <label className="field"><span>{st.reasonLabel || "Raison (optionnel)"}</span>
          <input autoFocus value={reason} onChange={(e) => setReason(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") done(true); }} />
        </label>
      ) : null}
    </Dialog>
  ) : null;

  return { confirm, node };
}

/* ---------------- Dialog ---------------- */
export function Dialog({ title, children, onClose, foot }: { title: string; children: ReactNode; onClose: () => void; foot?: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    ref.current?.focus();
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="dialog-bg" onClick={onClose}>
      <div className="dialog" role="dialog" aria-modal="true" aria-label={title} tabIndex={-1} ref={ref} onClick={(e) => e.stopPropagation()}>
        <h3>{title}</h3>
        {children}
        <div className="dialog-foot">
          {foot}
          <button className="btn secondary" onClick={onClose}>Fermer</button>
        </div>
      </div>
    </div>
  );
}
