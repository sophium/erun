import type { UISelection } from '@/types';

export interface TerminalExitSelections {
  sshdInitSelection?: UISelection;
  doctorSelection?: UISelection;
  openSelection?: UISelection;
  cloudInit: boolean;
}
