// Thin fetch wrapper for the control-api. Auth is handled upstream by
// oauth2-proxy; a 401 means the session expired → bounce to its sign-in.

export class ApiError extends Error {
  status: number;
  body: any;
  constructor(status: number, body: any) {
    super(body?.error || `HTTP ${status}`);
    this.status = status;
    this.body = body;
  }
}

async function parse(res: Response) {
  const text = await res.text();
  try {
    return text ? JSON.parse(text) : {};
  } catch {
    return { raw: text };
  }
}

// fetch() rejects (not resolves) on network failure / aborted connection. Wrap
// that as an ApiError(0) so callers get a consistent error shape (and a usable
// .message) instead of a raw TypeError.
async function doFetch(path: string, init: RequestInit): Promise<Response> {
  try {
    return await fetch(path, init);
  } catch (e: any) {
    throw new ApiError(0, { error: `Réseau injoignable: ${e?.message || "fetch failed"}` });
  }
}

export async function apiGet<T = any>(path: string): Promise<T> {
  const res = await doFetch(path, {
    credentials: "same-origin",
    // X-HL-CSRF is required by control-api's requireMutationAuth for role-gated
    // reads too (/v1/audit, /v1/configfile), not just mutations. Public reads
    // ignore it, so sending it always is safe and fixes those 403s.
    headers: { Accept: "application/json", "X-HL-CSRF": "1" },
  });
  if (res.status === 401) {
    window.location.href = "/oauth2/sign_in?rd=" + encodeURIComponent(window.location.pathname + window.location.search);
    throw new ApiError(401, {});
  }
  const body = await parse(res);
  if (!res.ok) throw new ApiError(res.status, body);
  return body as T;
}

export async function apiPost<T = any>(path: string, payload?: any): Promise<T> {
  const res = await doFetch(path, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", Accept: "application/json", "X-HL-CSRF": "1" },
    body: payload ? JSON.stringify(payload) : undefined,
  });
  if (res.status === 401) {
    window.location.href = "/oauth2/sign_in?rd=" + encodeURIComponent(window.location.pathname + window.location.search);
    throw new ApiError(401, {});
  }
  const body = await parse(res);
  if (!res.ok) throw new ApiError(res.status, body);
  return body as T;
}

// The control-api guards risky actions with a server-side double-confirm: the
// first POST returns HTTP 409 + { confirm: "double", confirm_id, message }. The
// client must re-POST the SAME payload with that confirm_id to actually fire.
// This helper surfaces the challenge to `confirm(message)`; if accepted it
// re-POSTs exactly once with confirm_id merged in. A second armed response is
// NOT retried — it surfaces as an ApiError so we never loop.
function isArmedChallenge(e: unknown): e is ApiError {
  return e instanceof ApiError && e.status === 409 && !!e.body?.confirm_id;
}

export async function apiPostConfirm<T = any>(
  path: string,
  payload: any,
  // Accepts a sync or async confirmer so callers can use an in-app modal
  // (window.confirm/prompt silently return false once Chrome's "prevent
  // additional dialogs" is ticked, breaking every later action).
  confirm: (message: string) => boolean | Promise<boolean>,
): Promise<T> {
  try {
    return await apiPost<T>(path, payload);
  } catch (e) {
    if (!isArmedChallenge(e)) throw e;
    const { confirm_id, message } = e.body;
    if (!(await confirm(message || "Confirmer cette action ?"))) return undefined as T;
    return await apiPost<T>(path, { ...payload, confirm_id });
  }
}
