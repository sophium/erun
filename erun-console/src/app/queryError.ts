import { isRecord } from 'erun-kit';

// describeQueryError reads message, status and envelope code from a platformApi error.
export function describeQueryError(error: unknown): {
  status?: number;
  code?: string;
  message: string;
} {
  if (isRecord(error)) {
    if (typeof error.status === 'number') {
      return {
        status: error.status,
        code: typeof error.code === 'string' ? error.code : undefined,
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
