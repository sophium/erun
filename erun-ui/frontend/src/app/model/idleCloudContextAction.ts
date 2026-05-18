import type { UIIdleStatus } from '@/types';

export interface IdleCloudContextAction {
  idleStatus: UIIdleStatus;
  operation: 'start' | 'stop';
  name: string;
  run: (name: string) => Promise<unknown>;
  label: string;
  refreshKubernetesContexts: boolean;
}
