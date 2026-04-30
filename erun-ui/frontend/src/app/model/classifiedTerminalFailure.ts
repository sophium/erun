import type { TerminalStatusAction } from '../state';
import type { UISelection } from '@/types';

export type ClassifiedTerminalFailure = {
  message: string;
  detail: string;
  action: TerminalStatusAction;
  retrySelection: UISelection | null;
};
