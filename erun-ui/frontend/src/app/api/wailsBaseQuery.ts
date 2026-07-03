import type { BaseQueryFn } from '@reduxjs/toolkit/query';

import { readError } from '../errors';

export interface WailsQueryError {
  message: string;
}

// NoValue stands in for `void` in generic positions, which
// @typescript-eslint/no-invalid-void-type forbids; the ReturnType wrapper
// captures the one place the rule permits `void` so the alias itself is legal.
type _VoidReturning = () => void;
export type NoValue = ReturnType<_VoidReturning>;

export type WailsQueryFn<Arg, Result> = (arg: Arg) => Promise<Result>;

export function wailsQueryFn<Arg, Result>(call: WailsQueryFn<Arg, Result>) {
  return async (arg: Arg): Promise<{ data: Result } | { error: WailsQueryError }> => {
    try {
      const data = await call(arg);
      return { data };
    } catch (error: unknown) {
      return { error: { message: readError(error) } };
    }
  };
}

// No shared HTTP transport exists: every endpoint supplies its own queryFn
// wrapping a Wails Go binding, so this base query is only a fallback guard.
export const wailsBaseQuery: BaseQueryFn<void, unknown, WailsQueryError> = (): Promise<{
  error: WailsQueryError;
}> =>
  Promise.resolve({
    error: {
      message: 'wailsBaseQuery must not be invoked directly; endpoints must define queryFn.',
    },
  });
