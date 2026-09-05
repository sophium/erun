import type { UISelection } from '@/types';

import type { CloudInitProvider } from './cloudInitProvider';

export interface TerminalExitSelections {
  openSelection?: UISelection;
  cloudInit: CloudInitProvider | null;
}
