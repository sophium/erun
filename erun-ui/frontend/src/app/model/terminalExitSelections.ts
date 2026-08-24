import type { UISelection } from '@/types';

import type { CloudInitProvider } from './cloudInitProvider';

export interface TerminalExitSelections {
  sshdInitSelection?: UISelection;
  openSelection?: UISelection;
  cloudInit: CloudInitProvider | null;
}
