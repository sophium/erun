import '@xterm/xterm/css/xterm.css';
import './style.css';

import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';

import { LoadDiff, LoadState, ResizeSession, SavePastedImage, SendSessionInput, StartSession } from '../wailsjs/go/main/App';
import { EventsOn, WindowToggleMaximise } from '../wailsjs/runtime/runtime';

interface UIEnvironment {
  name: string;
  mcpUrl?: string;
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

interface DiffResult {
  workingDirectory?: string;
  rawDiff: string;
  summary: DiffSummary;
  files?: DiffFile[];
  tree?: DiffTreeNode[];
}

interface DiffSummary {
  fileCount: number;
  additions: number;
  deletions: number;
}

interface DiffFile {
  path: string;
  oldPath?: string;
  newPath?: string;
  status: string;
  additions: number;
  deletions: number;
  binary?: boolean;
  hunks?: DiffHunk[];
}

interface DiffHunk {
  header: string;
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  lines?: DiffLine[];
}

interface DiffLine {
  kind: 'context' | 'add' | 'delete' | 'meta';
  content: string;
  oldLine?: number;
  newLine?: number;
}

interface DiffTreeNode {
  name: string;
  path: string;
  parentPath?: string;
  type: 'directory' | 'file';
  depth: number;
  status?: string;
  additions?: number;
  deletions?: number;
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
      <button id="review-toggle" class="titlebar__button titlebar__button--right" type="button" aria-label="Toggle diff panel">
        ${diffPanelIconMarkup()}
      </button>
      <button id="files-toggle" class="titlebar__button titlebar__button--right titlebar__button--files is-hidden" type="button" aria-label="Toggle changed files list">
        ${filesPanelIconMarkup()}
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
        <div id="terminal-view" class="terminal-view">
          <div id="terminal" class="terminal"></div>
          <div id="terminal-message" class="terminal-message is-hidden"></div>
        </div>
        <div id="review-splitter" class="review-splitter is-hidden" role="separator" aria-orientation="vertical" aria-label="Resize diff panel"></div>
        <section id="review-view" class="review-view is-hidden">
          <div id="review-main" class="review-main">
            <div id="diff-list" class="diff-list"></div>
          </div>
          <div id="files-splitter" class="files-splitter" role="separator" aria-orientation="vertical" aria-label="Resize changed files list"></div>
          <aside class="changed-files">
            <div class="changed-files__header">
              <button id="changed-files-toggle" class="changed-files__title" type="button">
                Changed files <span id="changed-files-count">0</span> ${chevronDownMarkup()}
              </button>
              <div class="changed-files__actions">
                <button id="diff-refresh" class="changed-files__icon-button" type="button" aria-label="Refresh diff">
                  ${refreshIconMarkup()}
                </button>
                <div class="changed-files__stats">
                  <span id="diff-additions">+0</span>
                  <span id="diff-deletions">-0</span>
                </div>
              </div>
            </div>
            <label class="file-filter">
              ${searchIconMarkup()}
              <input id="file-filter" type="search" placeholder="Filter files..." autocomplete="off" />
            </label>
            <div id="changed-file-tree" class="changed-file-tree"></div>
          </aside>
        </section>
      </main>
    </div>
  </div>
`;

const sidebarList = document.querySelector<HTMLDivElement>('#sidebar-list');
const sidebar = document.querySelector<HTMLElement>('#sidebar');
const splitter = document.querySelector<HTMLElement>('#splitter');
const sidebarToggle = document.querySelector<HTMLButtonElement>('#sidebar-toggle');
const reviewToggle = document.querySelector<HTMLButtonElement>('#review-toggle');
const titlebar = document.querySelector<HTMLElement>('#titlebar');
const terminalView = document.querySelector<HTMLDivElement>('#terminal-view');
const terminalRoot = document.querySelector<HTMLDivElement>('#terminal');
const reviewSplitter = document.querySelector<HTMLElement>('#review-splitter');
const reviewView = document.querySelector<HTMLElement>('#review-view');
const reviewMain = document.querySelector<HTMLDivElement>('#review-main');
const diffList = document.querySelector<HTMLDivElement>('#diff-list');
const changedFileTree = document.querySelector<HTMLDivElement>('#changed-file-tree');
const fileFilter = document.querySelector<HTMLInputElement>('#file-filter');
const filesToggle = document.querySelector<HTMLButtonElement>('#files-toggle');
const diffRefresh = document.querySelector<HTMLButtonElement>('#diff-refresh');
const filesSplitter = document.querySelector<HTMLElement>('#files-splitter');
const changedFilesCount = document.querySelector<HTMLSpanElement>('#changed-files-count');
const diffAdditions = document.querySelector<HTMLSpanElement>('#diff-additions');
const diffDeletions = document.querySelector<HTMLSpanElement>('#diff-deletions');
const terminalMessage = document.querySelector<HTMLDivElement>('#terminal-message');

if (!sidebarList || !sidebar || !splitter || !sidebarToggle || !reviewToggle || !titlebar || !terminalView || !terminalRoot || !reviewSplitter || !reviewView || !reviewMain || !diffList || !changedFileTree || !fileFilter || !filesToggle || !diffRefresh || !filesSplitter || !changedFilesCount || !diffAdditions || !diffDeletions || !terminalMessage) {
  throw new Error('required elements are missing');
}

const root = document.documentElement;
const MIN_SIDEBAR_WIDTH = 248;
const MAX_SIDEBAR_WIDTH = 520;
const DEFAULT_SIDEBAR_WIDTH = 338;
const MIN_REVIEW_WIDTH = 420;
const MAX_REVIEW_WIDTH = 920;
const DEFAULT_REVIEW_WIDTH = 620;
const MIN_FILES_WIDTH = 220;
const MAX_FILES_WIDTH = 460;
const DEFAULT_FILES_WIDTH = 300;
const SIDEBAR_WIDTH_STORAGE_KEY = 'erun.sidebarWidth';
const REVIEW_WIDTH_STORAGE_KEY = 'erun.reviewWidth';
const FILES_WIDTH_STORAGE_KEY = 'erun.filesWidth';
const FILES_OPEN_STORAGE_KEY = 'erun.filesOpen';

const state: {
  tenants: UITenant[];
  selected: UISelection | null;
  collapsed: Set<string>;
  sessionId: number;
  selectionSessions: Map<string, number>;
  sessionBuffers: Map<number, Uint8Array[]>;
  sessionExitReasons: Map<number, string>;
  sidebarWidth: number;
  reviewWidth: number;
  filesWidth: number;
  filesOpen: boolean;
  sidebarHidden: boolean;
  reviewOpen: boolean;
  diff: DiffResult | null;
  diffLoading: boolean;
  diffError: string;
  selectedDiffPath: string;
  diffFilter: string;
  collapsedDiffDirs: Set<string>;
} = {
  tenants: [],
  selected: null,
  collapsed: new Set<string>(),
  sessionId: 0,
  selectionSessions: new Map<string, number>(),
  sessionBuffers: new Map<number, Uint8Array[]>(),
  sessionExitReasons: new Map<number, string>(),
  sidebarWidth: loadSavedSidebarWidth(),
  reviewWidth: loadSavedReviewWidth(),
  filesWidth: loadSavedFilesWidth(),
  filesOpen: loadSavedFilesOpen(),
  sidebarHidden: false,
  reviewOpen: false,
  diff: null,
  diffLoading: false,
  diffError: '',
  selectedDiffPath: '',
  diffFilter: '',
  collapsedDiffDirs: new Set<string>(),
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
let reviewScrollFrame = 0;
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

reviewToggle.addEventListener('click', () => {
  setReviewOpen(!state.reviewOpen);
});

fileFilter.addEventListener('input', () => {
  state.diffFilter = fileFilter.value.trim().toLowerCase();
  renderChangedFileTree();
});

filesToggle.addEventListener('click', () => {
  setFilesOpen(!state.filesOpen);
});

diffRefresh.addEventListener('click', () => {
  void loadReviewDiff();
});

reviewMain.addEventListener('scroll', () => {
  queueVisibleDiffSelectionUpdate();
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

reviewSplitter.addEventListener('mousedown', (event: MouseEvent) => {
  if (!state.reviewOpen) {
    return;
  }
  event.preventDefault();
  document.body.classList.add('is-resizing-review');

  const move = (moveEvent: MouseEvent) => {
    const paneRect = terminalView.parentElement?.getBoundingClientRect();
    if (!paneRect) {
      return;
    }
    setReviewWidth(paneRect.right - moveEvent.clientX);
  };
  const stop = () => {
    document.body.classList.remove('is-resizing-review');
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', stop);
    saveReviewWidth();
  };

  window.addEventListener('mousemove', move);
  window.addEventListener('mouseup', stop);
});

filesSplitter.addEventListener('mousedown', (event: MouseEvent) => {
  if (!state.reviewOpen) {
    return;
  }
  event.preventDefault();
  document.body.classList.add('is-resizing-files');

  const move = (moveEvent: MouseEvent) => {
    const reviewRect = reviewView.getBoundingClientRect();
    setFilesWidth(reviewRect.right - moveEvent.clientX);
  };
  const stop = () => {
    document.body.classList.remove('is-resizing-files');
    window.removeEventListener('mousemove', move);
    window.removeEventListener('mouseup', stop);
    saveFilesWidth();
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
applyReviewWidth();
applyFilesWidth();
setFilesOpen(state.filesOpen, false);

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

  if (state.reviewOpen) {
    await loadReviewDiff();
  }
  terminal.focus();
  queueTerminalResize();
}

function setReviewOpen(open: boolean): void {
  state.reviewOpen = open;
  reviewToggle.classList.toggle('is-active', open);
  reviewToggle.setAttribute('aria-pressed', String(open));
  filesToggle.classList.toggle('is-hidden', !open);
  reviewSplitter.classList.toggle('is-hidden', !open);
  reviewView.classList.toggle('is-hidden', !open);
  terminalPaneReviewClass(open);
  setFilesOpen(state.filesOpen, false);
  applyReviewWidth();
  applyFilesWidth();
  queueTerminalResize();
  if (open) {
    void loadReviewDiff();
  }
  window.setTimeout(() => terminal.focus(), 0);
}

function terminalPaneReviewClass(open: boolean): void {
  terminalView.parentElement?.classList.toggle('has-review-panel', open);
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

function setReviewWidth(nextWidth: number): void {
  state.reviewWidth = clamp(nextWidth, MIN_REVIEW_WIDTH, MAX_REVIEW_WIDTH);
  applyReviewWidth();
  queueTerminalResize();
}

function setFilesWidth(nextWidth: number): void {
  state.filesWidth = clamp(nextWidth, MIN_FILES_WIDTH, MAX_FILES_WIDTH);
  applyFilesWidth();
}

function setFilesOpen(open: boolean, persist = true): void {
  state.filesOpen = open;
  filesToggle.classList.toggle('is-active', open);
  filesToggle.setAttribute('aria-pressed', String(open));
  filesSplitter.classList.toggle('is-hidden', !open);
  reviewView.classList.toggle('files-hidden', !open);
  applyFilesWidth();
  if (persist) {
    saveFilesOpen();
  }
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

function loadSavedReviewWidth(): number {
  try {
    const raw = window.localStorage.getItem(REVIEW_WIDTH_STORAGE_KEY);
    if (!raw) {
      return DEFAULT_REVIEW_WIDTH;
    }
    const parsed = Number.parseInt(raw, 10);
    if (!Number.isFinite(parsed)) {
      return DEFAULT_REVIEW_WIDTH;
    }
    return clamp(parsed, MIN_REVIEW_WIDTH, MAX_REVIEW_WIDTH);
  } catch {
    return DEFAULT_REVIEW_WIDTH;
  }
}

function saveReviewWidth(): void {
  try {
    window.localStorage.setItem(REVIEW_WIDTH_STORAGE_KEY, String(state.reviewWidth));
  } catch {
  }
}

function loadSavedFilesWidth(): number {
  try {
    const raw = window.localStorage.getItem(FILES_WIDTH_STORAGE_KEY);
    if (!raw) {
      return DEFAULT_FILES_WIDTH;
    }
    const parsed = Number.parseInt(raw, 10);
    if (!Number.isFinite(parsed)) {
      return DEFAULT_FILES_WIDTH;
    }
    return clamp(parsed, MIN_FILES_WIDTH, MAX_FILES_WIDTH);
  } catch {
    return DEFAULT_FILES_WIDTH;
  }
}

function saveFilesWidth(): void {
  try {
    window.localStorage.setItem(FILES_WIDTH_STORAGE_KEY, String(state.filesWidth));
  } catch {
  }
}

function loadSavedFilesOpen(): boolean {
  try {
    return window.localStorage.getItem(FILES_OPEN_STORAGE_KEY) !== 'false';
  } catch {
    return true;
  }
}

function saveFilesOpen(): void {
  try {
    window.localStorage.setItem(FILES_OPEN_STORAGE_KEY, String(state.filesOpen));
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

function applyReviewWidth(): void {
  const paneWidth = terminalView.parentElement?.getBoundingClientRect().width || 0;
  const maxForPane = paneWidth > 0 ? paneWidth - 370 : MAX_REVIEW_WIDTH;
  const maximum = Math.max(MIN_REVIEW_WIDTH, Math.min(MAX_REVIEW_WIDTH, maxForPane));
  root.style.setProperty('--review-width', `${clamp(state.reviewWidth, MIN_REVIEW_WIDTH, maximum)}px`);
}

function applyFilesWidth(): void {
  const reviewWidth = reviewView.getBoundingClientRect().width || state.reviewWidth;
  const maxForReview = reviewWidth > 0 ? reviewWidth - 260 : MAX_FILES_WIDTH;
  const maximum = Math.max(MIN_FILES_WIDTH, Math.min(MAX_FILES_WIDTH, maxForReview));
  root.style.setProperty('--files-width', `${clamp(state.filesWidth, MIN_FILES_WIDTH, maximum)}px`);
}

function queueTerminalResize(): void {
  window.clearTimeout(resizeTimer);
  resizeTimer = window.setTimeout(() => {
    applyReviewWidth();
    applyFilesWidth();
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

async function loadReviewDiff(): Promise<void> {
  if (!state.selected) {
    return;
  }
  state.diffLoading = true;
  state.diffError = '';
  renderReview();
  try {
    const diff = (await LoadDiff(state.selected)) as DiffResult;
    state.diff = diff;
    state.selectedDiffPath = chooseSelectedDiffPath(diff);
  } catch (error: unknown) {
    state.diff = null;
    state.diffError = readError(error);
  } finally {
    state.diffLoading = false;
    renderReview();
  }
}

function renderReview(): void {
  changedFilesCount.textContent = String(state.diff?.summary?.fileCount || 0);
  diffAdditions.textContent = `+${state.diff?.summary?.additions || 0}`;
  diffDeletions.textContent = `-${state.diff?.summary?.deletions || 0}`;
  diffRefresh.disabled = state.diffLoading;
  renderChangedFileTree();
  renderDiffList();
}

function renderChangedFileTree(): void {
  changedFileTree.replaceChildren();
  if (state.diffLoading) {
    changedFileTree.appendChild(statusElement('Loading...'));
    return;
  }
  if (state.diffError) {
    changedFileTree.appendChild(statusElement(compactDiffError(state.diffError)));
    return;
  }
  const tree = visibleDiffTreeNodes(filterDiffTree(state.diff?.tree || [], state.diffFilter));
  if (tree.length === 0) {
    changedFileTree.appendChild(statusElement(state.diff ? 'No matching files' : 'No changes'));
    return;
  }
  for (const node of tree) {
    changedFileTree.appendChild(renderDiffTreeNode(node));
  }
}

function renderDiffTreeNode(node: DiffTreeNode): HTMLElement {
  const wrapper = document.createElement('div');
  wrapper.className = 'changed-file-node';
  if (node.type === 'directory') {
    const collapsed = state.collapsedDiffDirs.has(node.path);
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'changed-file-row changed-file-row--directory';
    button.style.setProperty('--depth', String(node.depth));
    button.innerHTML = `${chevronMarkup(!collapsed)}<span>${escapeHTML(node.name)}</span>`;
    button.addEventListener('click', () => {
      if (collapsed) {
        state.collapsedDiffDirs.delete(node.path);
      } else {
        state.collapsedDiffDirs.add(node.path);
      }
      renderChangedFileTree();
    });
    wrapper.appendChild(button);
    return wrapper;
  }

  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'changed-file-row changed-file-row--file';
  if (node.path === state.selectedDiffPath) {
    button.classList.add('is-selected');
  }
  button.style.setProperty('--depth', String(node.depth));
  button.dataset.path = node.path;
  button.innerHTML = `${fileIconMarkup(node.path)}<span>${escapeHTML(node.name)}</span>`;
  button.addEventListener('click', () => {
    state.selectedDiffPath = node.path;
    renderReview();
    scrollSelectedDiffIntoView();
  });
  wrapper.appendChild(button);
  return wrapper;
}

function renderDiffList(): void {
  diffList.replaceChildren();
  if (state.diffLoading) {
    diffList.appendChild(statusElement('Loading diff...'));
    return;
  }
  if (state.diffError) {
    diffList.appendChild(statusElement(compactDiffError(state.diffError)));
    return;
  }
  const files = state.diff?.files || [];
  if (files.length === 0) {
    diffList.appendChild(statusElement('No changes'));
    return;
  }
  for (const file of files) {
    diffList.appendChild(renderDiffFile(file));
  }
}

function renderDiffFile(file: DiffFile): HTMLElement {
  const section = document.createElement('section');
  section.className = 'diff-file';
  section.dataset.path = file.path;
  if (file.path === state.selectedDiffPath) {
    section.classList.add('is-selected');
  }

  const header = document.createElement('header');
  header.className = 'diff-file__header';
  header.innerHTML = `
    <span class="diff-file__path">${escapeHTML(file.path)}</span>
    <span class="diff-file__counts"><span>+${file.additions}</span> <span>-${file.deletions}</span></span>
  `;
  section.appendChild(header);

  if (file.binary) {
    section.appendChild(statusElement('Binary file changed'));
    return section;
  }

  for (const hunk of file.hunks || []) {
    section.appendChild(renderDiffHunk(hunk));
  }
  return section;
}

function renderDiffHunk(hunk: DiffHunk): HTMLElement {
  const block = document.createElement('div');
  block.className = 'diff-hunk';

  const header = document.createElement('div');
  header.className = 'diff-hunk__header';
  header.textContent = hunk.header;
  block.appendChild(header);

  const body = document.createElement('div');
  body.className = 'diff-hunk__body';
  const contentWidth = Math.max(1, ...(hunk.lines || []).map((line) => line.content?.length || 0));
  body.style.setProperty('--diff-content-width', `${contentWidth + 2}ch`);
  for (const line of hunk.lines || []) {
    const row = document.createElement('div');
    row.className = `diff-line diff-line--${line.kind}`;
    row.innerHTML = `
      <span class="diff-line__old">${line.oldLine || ''}</span>
      <span class="diff-line__new">${line.newLine || ''}</span>
      <span class="diff-line__mark">${diffLineMark(line.kind)}</span>
      <span class="diff-line__content">${escapeHTML(line.content || ' ')}</span>
    `;
    body.appendChild(row);
  }
  block.appendChild(body);
  return block;
}

function filterDiffTree(nodes: DiffTreeNode[], filter: string): DiffTreeNode[] {
  if (!filter) {
    return nodes;
  }
  const matchingPaths = new Set<string>();
  const nodesByPath = new Map(nodes.map((node) => [node.path, node]));
  for (const node of nodes.filter((item) => item.type === 'file')) {
    if (!node.path.toLowerCase().includes(filter)) {
      continue;
    }
    matchingPaths.add(node.path);
    let parentPath = node.parentPath || '';
    while (parentPath) {
      matchingPaths.add(parentPath);
      parentPath = nodesByPath.get(parentPath)?.parentPath || '';
    }
  }
  return nodes.filter((node) => matchingPaths.has(node.path));
}

function visibleDiffTreeNodes(nodes: DiffTreeNode[]): DiffTreeNode[] {
  const nodesByPath = new Map(nodes.map((node) => [node.path, node]));
  return nodes.filter((node) => {
    let parentPath = node.parentPath || '';
    while (parentPath) {
      if (state.collapsedDiffDirs.has(parentPath)) {
        return false;
      }
      parentPath = nodesByPath.get(parentPath)?.parentPath || '';
    }
    return true;
  });
}

function chooseSelectedDiffPath(diff: DiffResult | null): string {
  const files = diff?.files || [];
  if (files.some((file) => file.path === state.selectedDiffPath)) {
    return state.selectedDiffPath;
  }
  return files[0]?.path || '';
}

function scrollSelectedDiffIntoView(): void {
  const selector = `[data-path="${cssEscape(state.selectedDiffPath)}"]`;
  diffList.querySelector<HTMLElement>(selector)?.scrollIntoView({block: 'start', behavior: 'smooth'});
}

function queueVisibleDiffSelectionUpdate(): void {
  if (reviewScrollFrame > 0) {
    return;
  }
  reviewScrollFrame = window.requestAnimationFrame(() => {
    reviewScrollFrame = 0;
    updateSelectedDiffPathFromScroll();
  });
}

function updateSelectedDiffPathFromScroll(): void {
  const path = visibleDiffPath();
  if (!path || path === state.selectedDiffPath) {
    return;
  }
  state.selectedDiffPath = path;
  updateChangedFileSelection();
}

function visibleDiffPath(): string {
  const sections = Array.from(diffList.querySelectorAll<HTMLElement>('.diff-file[data-path]'));
  if (sections.length === 0) {
    return '';
  }

  const containerRect = reviewMain.getBoundingClientRect();
  const anchor = containerRect.top + 72;
  let closestPath = '';
  let closestDistance = Number.POSITIVE_INFINITY;

  for (const section of sections) {
    const rect = section.getBoundingClientRect();
    const path = section.dataset.path || '';
    if (!path) {
      continue;
    }
    if (rect.top <= anchor && rect.bottom > anchor) {
      return path;
    }
    const distance = Math.abs(rect.top - anchor);
    if (distance < closestDistance) {
      closestDistance = distance;
      closestPath = path;
    }
  }
  return closestPath;
}

function updateChangedFileSelection(): void {
  changedFileTree.querySelectorAll<HTMLElement>('.changed-file-row.is-selected').forEach((row) => {
    row.classList.remove('is-selected');
  });

  const selector = `.changed-file-row--file[data-path="${cssEscape(state.selectedDiffPath)}"]`;
  const row = changedFileTree.querySelector<HTMLElement>(selector);
  if (!row) {
    return;
  }
  row.classList.add('is-selected');
  row.scrollIntoView({block: 'nearest'});
}

function statusElement(message: string): HTMLDivElement {
  const element = document.createElement('div');
  element.className = 'review-status';
  element.textContent = message;
  return element;
}

function compactDiffError(message: string): string {
  if (message.includes('unknown tool "diff"')) {
    return 'Runtime MCP does not expose diff yet. Refresh after deploy finishes.';
  }
  return message;
}

function diffLineMark(kind: DiffLine['kind']): string {
  if (kind === 'add') {
    return '+';
  }
  if (kind === 'delete') {
    return '-';
  }
  return '';
}

function cssEscape(value: string): string {
  if ('CSS' in window && typeof window.CSS.escape === 'function') {
    return window.CSS.escape(value);
  }
  return value.replaceAll('"', '\\"');
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

function diffPanelIconMarkup(): string {
  return `
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <rect x="2.75" y="3" width="14.5" height="14" rx="3" fill="none" stroke="currentColor" stroke-width="1.7"/>
      <path d="M10 4.5v11" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/>
    </svg>
  `;
}

function filesPanelIconMarkup(): string {
  return `
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="M5.25 6.5V5.8A2.3 2.3 0 0 1 7.55 3.5h2.35c.5 0 .98.17 1.36.48l.9.74c.22.18.49.28.78.28h2.05a2.3 2.3 0 0 1 2.3 2.3v5.95a2.3 2.3 0 0 1-2.3 2.3h-.75" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
      <path d="M2.75 8.05A2.3 2.3 0 0 1 5.05 5.75H7.4c.5 0 .98.17 1.36.48l.9.74c.22.18.49.28.78.28h2.05a2.3 2.3 0 0 1 2.3 2.3v4.65a2.3 2.3 0 0 1-2.3 2.3H5.05a2.3 2.3 0 0 1-2.3-2.3V8.05Z" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"/>
    </svg>
  `;
}

function refreshIconMarkup(): string {
  return `
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="M15.2 7.2A5.7 5.7 0 0 0 5 5.3L3.6 6.7" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
      <path d="M3.5 3.7v3.1h3.1" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
      <path d="M4.8 12.8A5.7 5.7 0 0 0 15 14.7l1.4-1.4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
      <path d="M16.5 16.3v-3.1h-3.1" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
    </svg>
  `;
}

function searchIconMarkup(): string {
  return `
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <circle cx="8.5" cy="8.5" r="5" fill="none" stroke="currentColor" stroke-width="1.5"/>
      <path d="m12.3 12.3 3.2 3.2" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
    </svg>
  `;
}

function chevronDownMarkup(): string {
  return `
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <path d="m6 8 4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
    </svg>
  `;
}

function chevronMarkup(open: boolean): string {
  return `
    <svg class="tree-chevron ${open ? 'is-open' : ''}" viewBox="0 0 20 20" aria-hidden="true">
      <path d="m8 5 5 5-5 5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
    </svg>
  `;
}

function fileIconMarkup(filePath: string): string {
  const extension = filePath.split('.').pop()?.toLowerCase() || '';
  const label = extension === 'go' ? 'GO' : extension === 'json' ? '{}' : extension === 'rb' ? 'RB' : '';
  return `<span class="file-icon" aria-hidden="true">${escapeHTML(label)}</span>`;
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
