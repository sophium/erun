import '@xterm/xterm/css/xterm.css';
import './style.css';

import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';

import { LoadState, ResizeSession, SavePastedImage, SendSessionInput, StartSession } from '../wailsjs/go/main/App';
import { EventsOn, WindowToggleMaximise } from '../wailsjs/runtime/runtime';

interface UIEnvironment {
  name: string;
}

interface UITenant {
  name: string;
  environments: UIEnvironment[];
}

interface UISelection {
  tenant: string;
  environment: string;
}

interface UIState {
  tenants: UITenant[];
  selected?: UISelection;
  message?: string;
}

interface StartSessionResult {
  sessionId: number;
  selection: UISelection;
}

interface TerminalOutputPayload {
  sessionId: number;
  data: string;
}

interface TerminalExitPayload {
  sessionId: number;
  reason?: string;
}

interface PastedImageResult {
  path: string;
}

const shell = document.querySelector<HTMLDivElement>('#app');
if (!shell) {
  throw new Error('app root not found');
}

shell.innerHTML = `
  <div class="app-shell">
    <header id="titlebar" class="titlebar" data-wails-drag>
      <button id="sidebar-toggle" class="titlebar__button" type="button" aria-label="Toggle sidebar">
        ${sidebarToggleMarkup()}
      </button>
      <div class="titlebar__fill" data-wails-drag></div>
    </header>
    <div class="workbench">
      <aside id="sidebar" class="sidebar">
        <div class="sidebar__header">
          <span class="sidebar__header-title">Environments</span>
        </div>
        <div id="sidebar-list" class="sidebar__list"></div>
      </aside>
      <div id="splitter" class="splitter" role="separator" aria-orientation="vertical" aria-label="Resize sidebar"></div>
      <main class="terminal-pane">
        <div id="terminal" class="terminal"></div>
        <div id="terminal-message" class="terminal-message is-hidden"></div>
      </main>
    </div>
  </div>
`;

const sidebarList = document.querySelector<HTMLDivElement>('#sidebar-list');
const sidebar = document.querySelector<HTMLElement>('#sidebar');
const splitter = document.querySelector<HTMLElement>('#splitter');
const sidebarToggle = document.querySelector<HTMLButtonElement>('#sidebar-toggle');
const titlebar = document.querySelector<HTMLElement>('#titlebar');
const terminalRoot = document.querySelector<HTMLDivElement>('#terminal');
const terminalMessage = document.querySelector<HTMLDivElement>('#terminal-message');

if (!sidebarList || !sidebar || !splitter || !sidebarToggle || !titlebar || !terminalRoot || !terminalMessage) {
  throw new Error('required elements are missing');
}

const root = document.documentElement;
const MIN_SIDEBAR_WIDTH = 248;
const MAX_SIDEBAR_WIDTH = 520;
const DEFAULT_SIDEBAR_WIDTH = 338;
const SIDEBAR_WIDTH_STORAGE_KEY = 'erun.sidebarWidth';

const state: {
  tenants: UITenant[];
  selected: UISelection | null;
  collapsed: Set<string>;
  sessionId: number;
  selectionSessions: Map<string, number>;
  sessionBuffers: Map<number, Uint8Array[]>;
  sessionExitReasons: Map<number, string>;
  sidebarWidth: number;
  sidebarHidden: boolean;
} = {
  tenants: [],
  selected: null,
  collapsed: new Set<string>(),
  sessionId: 0,
  selectionSessions: new Map<string, number>(),
  sessionBuffers: new Map<number, Uint8Array[]>(),
  sessionExitReasons: new Map<number, string>(),
  sidebarWidth: loadSavedSidebarWidth(),
  sidebarHidden: false,
};

const terminal = new Terminal({
  allowProposedApi: false,
  cursorBlink: true,
  fontFamily: 'ui-monospace, SFMono-Regular, SF Mono, Menlo, Monaco, Consolas, Liberation Mono, monospace',
  fontSize: 13,
  lineHeight: 1.18,
  theme: {
    background: '#000000',
  },
});
const fitAddon = new FitAddon();
terminal.loadAddon(fitAddon);
terminal.open(terminalRoot);
fitAddon.fit();

terminal.onData((data) => {
  SendSessionInput(data).catch((error: unknown) => {
    showTerminalMessage(readError(error));
  });
});

terminalRoot.addEventListener('paste', (event: ClipboardEvent) => {
  void handleTerminalPaste(event).catch((error: unknown) => {
    showTerminalMessage(readError(error));
  });
}, true);

let resizeTimer = 0;
const resizeObserver = new ResizeObserver(() => {
  queueTerminalResize();
});
resizeObserver.observe(terminalRoot);
window.addEventListener('resize', () => {
  queueTerminalResize();
});

sidebarToggle.addEventListener('click', () => {
  setSidebarHidden(!state.sidebarHidden);
});

titlebar.addEventListener('dblclick', (event: MouseEvent) => {
  const target = event.target;
  if (target instanceof HTMLElement && target.closest('button')) {
    return;
  }
  WindowToggleMaximise();
});

splitter.addEventListener('mousedown', (event: MouseEvent) => {
  if (state.sidebarHidden) {
    return;
  }
  event.preventDefault();
  document.body.classList.add('is-resizing');

  const move = (moveEvent: MouseEvent) => {
    setSidebarWidth(moveEvent.clientX);
  };
  const stop = () => {
    document.body.classList.remove('is-resizing');
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', stop);
    saveSidebarWidth();
  };

  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', stop);
});

EventsOn('terminal-output', (payload: TerminalOutputPayload) => {
  if (!payload) {
    return;
  }
  const data = decodeBase64Bytes(payload.data);
  const existing = state.sessionBuffers.get(payload.sessionId) || [];
  existing.push(data);
  state.sessionBuffers.set(payload.sessionId, existing);
  if (payload.sessionId !== state.sessionId) {
    return;
  }
  terminal.write(data);
});

EventsOn('terminal-exit', (payload: TerminalExitPayload) => {
  if (!payload) {
    return;
  }
  state.sessionExitReasons.set(payload.sessionId, payload.reason || 'Session ended.');
  if (payload.sessionId !== state.sessionId) {
    return;
  }
  showTerminalMessage(payload.reason || 'Session ended.');
});

void boot();
applySidebarState();

async function boot(): Promise<void> {
  try {
    const loaded = (await LoadState()) as UIState;
    state.tenants = loaded.tenants || [];
    state.selected = loaded.selected || null;
    renderSidebar();

    if (loaded.message) {
      showTerminalMessage(loaded.message);
      return;
    }

    if (state.selected) {
      await openSelection(state.selected);
      return;
    }

    showTerminalMessage('Choose an environment from the left pane.');
  } catch (error: unknown) {
    showTerminalMessage(readError(error));
  }
}

async function openSelection(selection: UISelection): Promise<void> {
  const key = selectionKey(selection);
  const previousSessionId = state.sessionId;
  const previousKnownSessionId = state.selectionSessions.get(key) || 0;

  state.selected = selection;
  renderSidebar();
  if (previousKnownSessionId === 0 || previousKnownSessionId !== previousSessionId) {
    showTerminalMessage(`Opening ${selection.tenant} / ${selection.environment}...`);
  }

  fitAddon.fit();
  const result = (await StartSession(selection, terminal.cols, terminal.rows)) as StartSessionResult;
  state.selectionSessions.set(key, result.sessionId);
  state.sessionId = result.sessionId;

  if (result.sessionId !== previousSessionId) {
    resetTerminal();
    const buffer = state.sessionBuffers.get(result.sessionId);
    if (buffer) {
      writeTerminalBuffer(buffer);
    }
  }

  const exitReason = state.sessionExitReasons.get(result.sessionId);
  if (exitReason) {
    showTerminalMessage(exitReason);
  } else {
    hideTerminalMessage();
  }

  terminal.focus();
  queueTerminalResize();
}

function renderSidebar(): void {
  sidebarList.replaceChildren();

  state.tenants.forEach((tenant, index) => {
    const group = document.createElement('div');
    group.className = 'tenant-group';
    if (index > 0) {
      group.classList.add('tenant-group--spaced');
    }

    const branchButton = document.createElement('button');
    branchButton.className = 'tenant-row';
    branchButton.type = 'button';
    branchButton.innerHTML = `${folderIconMarkup()}<span>${escapeHTML(tenant.name)}</span>`;
    branchButton.addEventListener('click', () => {
      if (state.collapsed.has(tenant.name)) {
        state.collapsed.delete(tenant.name);
      } else {
        state.collapsed.add(tenant.name);
      }
      renderSidebar();
    });
    group.appendChild(branchButton);

    if (!state.collapsed.has(tenant.name)) {
      const environmentList = document.createElement('div');
      environmentList.className = 'environment-list';

      tenant.environments.forEach((environment) => {
        const environmentButton = document.createElement('button');
        environmentButton.type = 'button';
        environmentButton.className = 'environment-row';

        const isSelected =
          state.selected?.tenant === tenant.name &&
          state.selected?.environment === environment.name;
        if (isSelected) {
          environmentButton.classList.add('is-selected');
        }

        environmentButton.textContent = environment.name;
        environmentButton.addEventListener('click', () => {
          void openSelection({
            tenant: tenant.name,
            environment: environment.name,
          }).catch((error: unknown) => {
            showTerminalMessage(readError(error));
          });
        });
        environmentList.appendChild(environmentButton);
      });

      group.appendChild(environmentList);
    }

    sidebarList.appendChild(group);
  });
}

function resetTerminal(): void {
  terminal.reset();
  terminal.clear();
}

function setSidebarWidth(nextWidth: number): void {
  state.sidebarWidth = clamp(nextWidth, MIN_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH);
  applySidebarState();
}

function loadSavedSidebarWidth(): number {
  try {
    const raw = window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY);
    if (!raw) {
      return DEFAULT_SIDEBAR_WIDTH;
    }
    const parsed = Number.parseInt(raw, 10);
    if (!Number.isFinite(parsed)) {
      return DEFAULT_SIDEBAR_WIDTH;
    }
    return clamp(parsed, MIN_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH);
  } catch {
    return DEFAULT_SIDEBAR_WIDTH;
  }
}

function saveSidebarWidth(): void {
  try {
    window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, String(state.sidebarWidth));
  } catch {
  }
}

function setSidebarHidden(hidden: boolean): void {
  state.sidebarHidden = hidden;
  applySidebarState();
  queueTerminalResize();
  window.setTimeout(() => {
    terminal.focus();
  }, 0);
}

function applySidebarState(): void {
  root.style.setProperty('--sidebar-width', `${state.sidebarHidden ? 0 : state.sidebarWidth}px`);
  shell.classList.toggle('sidebar-hidden', state.sidebarHidden);
  sidebarToggle.setAttribute('aria-pressed', String(!state.sidebarHidden));
}

function queueTerminalResize(): void {
  window.clearTimeout(resizeTimer);
  resizeTimer = window.setTimeout(() => {
    fitAddon.fit();
    if (state.sessionId > 0) {
      ResizeSession(terminal.cols, terminal.rows).catch(() => {
      });
    }
  }, 40);
}

function showTerminalMessage(message: string): void {
  terminalMessage.textContent = message;
  terminalMessage.classList.remove('is-hidden');
}

function hideTerminalMessage(): void {
  terminalMessage.textContent = '';
  terminalMessage.classList.add('is-hidden');
}

async function handleTerminalPaste(event: ClipboardEvent): Promise<void> {
  if (!isTerminalPasteTarget(event.target)) {
    return;
  }

  const images = pastedImageFiles(event);
  if (images.length === 0) {
    return;
  }

  event.preventDefault();
  const paths: string[] = [];
  for (const image of images) {
    const result = (await SavePastedImage({
      data: await fileToBase64(image),
      mimeType: image.type,
      name: image.name,
    })) as PastedImageResult;
    if (result.path) {
      paths.push(result.path);
    }
  }
  if (paths.length === 0) {
    return;
  }
  await SendSessionInput(`${paths.join(' ')} `);
  terminal.focus();
}

function isTerminalPasteTarget(target: EventTarget | null): boolean {
  return target instanceof Node && terminalRoot.contains(target);
}

function pastedImageFiles(event: ClipboardEvent): File[] {
  const items = event.clipboardData?.items;
  if (!items) {
    return [];
  }

  const files: File[] = [];
  for (const item of Array.from(items)) {
    if (item.kind !== 'file' || !item.type.toLowerCase().startsWith('image/')) {
      continue;
    }
    const file = item.getAsFile();
    if (file) {
      files.push(file);
    }
  }
  return files;
}

async function fileToBase64(file: File): Promise<string> {
  const buffer = await file.arrayBuffer();
  return bytesToBase64(new Uint8Array(buffer));
}

function bytesToBase64(bytes: Uint8Array): string {
  const chunkSize = 0x8000;
  let binary = '';
  for (let index = 0; index < bytes.length; index += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(index, index + chunkSize));
  }
  return window.btoa(binary);
}

function readError(error: unknown): string {
  if (typeof error === 'string') {
    return error;
  }
  if (error instanceof Error && typeof error.message === 'string') {
    return error.message;
  }
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') {
    return error.message;
  }
  return 'Unexpected error';
}

function decodeBase64Bytes(value: string): Uint8Array {
  const binary = window.atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

function writeTerminalBuffer(chunks: Uint8Array[]): void {
  for (const chunk of chunks) {
    terminal.write(chunk);
  }
}

function escapeHTML(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function folderIconMarkup(): string {
  return `
    <svg class="folder-icon" viewBox="0 0 24 24" aria-hidden="true">
      <path fill="currentColor" d="M3.75 5.5A2.25 2.25 0 0 1 6 3.25h3.45c.56 0 1.1.21 1.51.6l1.24 1.15c.14.13.33.2.52.2H18A2.25 2.25 0 0 1 20.25 7.45v8.8A2.5 2.5 0 0 1 17.75 18.75h-11A2.5 2.5 0 0 1 4.25 16.25V6.75A1.25 1.25 0 0 1 5.5 5.5h-1.75Z"/>
    </svg>
  `;
}

function sidebarToggleMarkup(): string {
  return `
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <rect x="2.25" y="3.25" width="15.5" height="13.5" rx="3" ry="3" fill="none" stroke="currentColor" stroke-width="1.5"/>
      <path d="M7.5 4.5v11" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
    </svg>
  `;
}

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value));
}

function selectionKey(selection: UISelection): string {
  return `${selection.tenant}\u0000${selection.environment}`;
}
