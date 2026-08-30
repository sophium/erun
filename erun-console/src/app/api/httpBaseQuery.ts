// httpBaseQuery is the console's transport for the shared `PlatformBaseQuery`
// contract (erun-kit): a real `fetch` against erun-backend-api, carrying the
// caller's bearer token and the endpoint's own "X request failed (status)"
// wording as a fallback. It is the console's counterpart to the desktop's
// wailsBaseQuery (erun-ui/frontend/src/app/api/wailsBaseQuery.ts) — same
// contract, a different transport underneath.
import { isRecord, type PlatformApiRequest, type PlatformBaseQuery } from 'erun-kit';

// No separate BFF in this increment — the console calls the auth-carrying
// erun-backend-api directly. Same-origin default lets the SPA sit behind the
// same auth edge that fronts the API.
const API_BASE = import.meta.env.VITE_API_BASE ?? '';

// parsedError prefers the backend's own {code, message} envelope (see
// erun-backend-api/internal/routes/errors.go, and auth.go's authErrorEnvelope
// for the pre-route auth layer -- both 401 and 403 now carry it too) over the
// generic fallback, so a caller sees why a request was rejected (e.g. which
// field failed validation, or TENANT_UNRESOLVED vs NOT_ENROLLED) rather than
// only its status code. Falls back to a message-only result when the body
// isn't that envelope.
async function parsedError(
  response: Response,
  label: string | undefined,
): Promise<{ message: string; code?: string }> {
  const fallback = { message: `${label ?? 'request'} failed (${String(response.status)})` };
  try {
    const body: unknown = await response.json();
    if (!isRecord(body) || typeof body.message !== 'string' || body.message.length === 0) {
      return fallback;
    }
    return typeof body.code === 'string' && body.code.length > 0
      ? { message: body.message, code: body.code }
      : { message: body.message };
  } catch {
    return fallback;
  }
}

function headersFor(token: string | undefined, hasBody: boolean): Record<string, string> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (token !== undefined) {
    headers.Authorization = `Bearer ${token}`;
  }
  if (hasBody) {
    headers['Content-Type'] = 'application/json';
  }
  return headers;
}

export const httpBaseQuery: PlatformBaseQuery = async ({
  url,
  method,
  body,
  token,
  label,
}: PlatformApiRequest) => {
  let response: Response;
  try {
    response = await fetch(`${API_BASE}${url}`, {
      method: method ?? 'GET',
      headers: headersFor(token, body !== undefined),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    return { error: { message: 'could not reach the erun API' } };
  }

  if (!response.ok) {
    return {
      error: {
        ...(await parsedError(response, label)),
        status: response.status,
      },
    };
  }

  if (response.status === 204) {
    return { data: undefined };
  }

  try {
    return { data: await response.json() };
  } catch {
    return { error: { message: `${label ?? 'request'} response was not in the expected shape` } };
  }
};
