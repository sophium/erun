import type {
  DiffResult,
  EnvironmentActionMode,
  ManageTab,
  UISelection,
  UITenant,
  UIVersionSuggestion,
} from '@/types';

export const MIN_SIDEBAR_WIDTH = 248;
export const MAX_SIDEBAR_WIDTH = 520;
export const DEFAULT_SIDEBAR_WIDTH = 338;
export const MIN_REVIEW_WIDTH = 420;
export const MAX_REVIEW_WIDTH = 920;
export const DEFAULT_REVIEW_WIDTH = 620;
export const MIN_FILES_WIDTH = 220;
export const MAX_FILES_WIDTH = 460;
export const DEFAULT_FILES_WIDTH = 300;
export const SIDEBAR_WIDTH_STORAGE_KEY = 'erun.sidebarWidth';
export const REVIEW_WIDTH_STORAGE_KEY = 'erun.reviewWidth';
export const FILES_WIDTH_STORAGE_KEY = 'erun.filesWidth';
export const FILES_OPEN_STORAGE_KEY = 'erun.filesOpen';

export interface EnvironmentDialogState {
  open: boolean;
  actionMode: EnvironmentActionMode;
  tenant: string;
  environment: string;
  version: string;
  noGit: boolean;
  versionImage: string;
  choicesOpen: boolean;
}

export interface ManageDialogState {
  open: boolean;
  tab: ManageTab;
  selection: UISelection | null;
  version: string;
  versionImage: string;
  confirmation: string;
  busy: boolean;
  choicesOpen: boolean;
}

export interface AppState {
  tenants: UITenant[];
  selected: UISelection | null;
  versionSuggestions: UIVersionSuggestion[];
  environmentDialog: EnvironmentDialogState;
  manageDialog: ManageDialogState;
  collapsedTenants: Set<string>;
  sessionId: number;
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
  terminalMessage: string;
}

export const defaultEnvironmentDialog = (): EnvironmentDialogState => ({
  open: false,
  actionMode: 'init',
  tenant: '',
  environment: '',
  version: '',
  noGit: false,
  versionImage: '',
  choicesOpen: false,
});

export const defaultManageDialog = (): ManageDialogState => ({
  open: false,
  tab: 'deploy',
  selection: null,
  version: '',
  versionImage: '',
  confirmation: '',
  busy: false,
  choicesOpen: false,
});
