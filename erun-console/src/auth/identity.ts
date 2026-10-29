// readTokenIdentity decodes display-only claims without verifying. Never use it for
// an authorization decision.

export interface TokenIdentity {
  subject: string | undefined;
  email: string | undefined;
  issuer: string | undefined;
}

const EMPTY: TokenIdentity = { subject: undefined, email: undefined, issuer: undefined };

function asOptionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}

// decodePayload returns undefined for anything that is not a decodable three-part JWT.
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
    // Zitadel puts the sign-in address in preferred_username.
    email: asOptionalString(payload.email) ?? asOptionalString(payload.preferred_username),
    issuer: asOptionalString(payload.iss),
  };
}
