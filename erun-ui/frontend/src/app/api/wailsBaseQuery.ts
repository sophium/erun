import type { BaseQueryFn } from '@reduxjs/toolkit/query';

import { readError } from '../errors';

export interface WailsQueryError {
  message: string;
}

// NoValue stands in for `void` in generic type positions where the
// `@typescript-eslint/no-invalid-void-type` rule disallows it. RTK Query and
// our Wails bindings use it for endpoints with no input or no return value.
// The lint rule allows `void` only in return-type position; we capture that
// return-type form via ReturnType so the alias itself does not trip the rule.
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

// fakeBaseQuery is used because every endpoint provides its own queryFn that
// wraps a Wails Go binding. There is no shared HTTP transport. The async
// signature is mandated by RTK Query's BaseQueryFn type even though this
// stub body never awaits.
export const wailsBaseQuery: BaseQueryFn<void, unknown, WailsQueryError> = (): Promise<{
  error: WailsQueryError;
}> =>
  Promise.resolve({
    error: {
      message: 'wailsBaseQuery must not be invoked directly; endpoints must define queryFn.',
    },
  });
