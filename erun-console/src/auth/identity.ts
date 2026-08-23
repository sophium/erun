// readTokenIdentity pulls the display-only claims out of a bearer token, for the
// "authenticated but not enrolled" screen. Someone in that state has to tell an
// operator WHICH identity to enrol, and reading it off the screen beats an
// operator querying the database for it — which is exactly what happened the
// first time this came up (#1167).
//
// This decodes without verifying, and that is safe only because the result is
// never used for a security decision: it is text on a screen. The API remains
// the only thing that decides what an identity may do. Do not grow this into an
// authorization input.

export interface TokenIdentity {
  subject: string | undefined;
  email: string | undefined;
  issuer: string | undefined;
}

const EMPTY: TokenIdentity = { subject: undefined, email: undefined, issuer: undefined };

function asOptionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}

// decodePayload returns the JWT's middle segment as parsed JSON, or undefined
// for anything that is not a decodable three-part JWT — an opaque token, a dev
// bearer string, or a truncated value. A token we cannot read is not an error
// here: the screen simply omits the identity line.
function decodePayload(token: string): Record<string, unknown> | undefined {
  const segments = token.split('.');
  const payload = segments.length === 3 ? segments[1] : undefined;
  if (payload === undefined) {
    return undefined;
  }
  const base64 = payload.replace(/-/g, '+').replace(/_/g, '/');
  const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), '=');
  try {
    const bytes = Uint8Array.from(atob(padded), (character) => character.charCodeAt(0));
    const parsed: unknown = JSON.parse(new TextDecoder().decode(bytes));
    return typeof parsed === 'object' && parsed !== null
      ? (parsed as Record<string, unknown>)
      : undefined;
  } catch {
    return undefined;
  }
}

export function readTokenIdentity(token: string): TokenIdentity {
  const payload = decodePayload(token);
  if (payload === undefined) {
    return EMPTY;
  }
  return {
    subject: asOptionalString(payload.sub),
    // preferred_username is what Zitadel puts the sign-in address in; email is
    // the standard claim. Either is more recognisable to a person than `sub`.
    email: asOptionalString(payload.email) ?? asOptionalString(payload.preferred_username),
    issuer: asOptionalString(payload.iss),
  };
}
