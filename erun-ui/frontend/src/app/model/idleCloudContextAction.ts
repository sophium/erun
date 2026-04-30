import type { UIIdleStatus } from '@/types';

export type IdleCloudContextAction = {
  idleStatus: UIIdleStatus;
  operation: 'start' | 'stop';
  name: string;
  run: (name: string) => Promise<unknown>;
  label: string;
  refreshKubernetesContexts: boolean;
};
