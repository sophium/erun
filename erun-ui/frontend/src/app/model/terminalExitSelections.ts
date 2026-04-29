import type { UISelection } from '@/types';

export type TerminalExitSelections = {
  initSelection?: UISelection;
  deploySelection?: UISelection;
  sshdInitSelection?: UISelection;
  doctorSelection?: UISelection;
  openSelection?: UISelection;
  cloudInit: boolean;
};
