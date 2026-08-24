// httpBaseQuery is the console's transport for the shared `PlatformBaseQuery`
// contract (erun-kit): a real `fetch` against erun-backend-api, carrying the
// caller's bearer token and the endpoint's own "X request failed (status)"
// wording. It is the console's counterpart to the desktop's wailsBaseQuery
// (erun-ui/frontend/src/app/api/wailsBaseQuery.ts) — same contract, a
// different transport underneath.
import type { PlatformApiRequest, PlatformBaseQuery } from 'erun-kit';

// No separate BFF in this increment — the console calls the auth-carrying
// erun-backend-api directly. Same-origin default lets the SPA sit behind the
// same auth edge that fronts the API.
const API_BASE = import.meta.env.VITE_API_BASE ?? '';

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
        message: `${label ?? 'request'} failed (${String(response.status)})`,
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
