import type { UISelection } from '@/types';

import type { TerminalStatusAction } from '../state';

export interface ClassifiedTerminalFailure {
  message: string;
  detail: string;
  action: TerminalStatusAction;
  retrySelection: UISelection | null;
}
