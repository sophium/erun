import type { UISelection } from '@/types';

export type TerminalExitSelections = {
  sshdInitSelection?: UISelection;
  doctorSelection?: UISelection;
  openSelection?: UISelection;
  cloudInit: boolean;
};
