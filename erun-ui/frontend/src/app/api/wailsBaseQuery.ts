import type { BaseQueryFn } from '@reduxjs/toolkit/query';

import { readError } from '../errors';

export type WailsQueryError = { message: string };

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

// fakeBaseQuery is used because every endpoint provides its own queryFn that
// wraps a Wails Go binding. There is no shared HTTP transport.
export const wailsBaseQuery: BaseQueryFn<void, unknown, WailsQueryError> = async () => ({
  error: { message: 'wailsBaseQuery must not be invoked directly; endpoints must define queryFn.' },
});
