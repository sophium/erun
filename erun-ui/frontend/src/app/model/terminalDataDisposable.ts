import type { Terminal } from '@xterm/xterm';

export type TerminalDataDisposable = ReturnType<Terminal['onData']>;
