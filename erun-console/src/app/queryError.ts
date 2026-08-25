import { isRecord } from 'erun-kit';

// Reads a human message (and, where present, an HTTP status) out of whatever
// shape a platformApi RTK Query mutation/query rejected or errored with: the
// shared PlatformApiError ({message, status}) for a transport/HTTP failure,
// or RTK Query's own thrown-in-transformResponse shape for a malformed body.
export function describeQueryError(error: unknown): { status?: number; message: string } {
  if (isRecord(error)) {
    if (typeof error.status === 'number') {
      return {
        status: error.status,
        message: typeof error.message === 'string' ? error.message : 'unexpected error',
      };
    }
    if (typeof error.message === 'string') {
      return { message: error.message };
    }
    if (typeof error.error === 'string') {
      return { message: error.error };
    }
  }
  return { message: 'unexpected error' };
}

export function queryErrorMessage(error: unknown): string {
  return describeQueryError(error).message;
}
