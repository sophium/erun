// httpBaseQuery is the console's fetch transport for erun-kit's PlatformBaseQuery contract.
import { isRecord, type PlatformApiRequest, type PlatformBaseQuery } from 'erun-kit';

// Same-origin default lets the SPA sit behind the auth edge that fronts the API.
const API_BASE = import.meta.env.VITE_API_BASE ?? '';

// parsedError prefers the backend's {code, message} envelope over the generic fallback.
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
