import type { EnvironmentType } from '@/types';

export interface NormalizedEnvironmentDialogValues {
  tenant: string;
  environment: string;
  version: string;
  kubernetesContext: string;
  containerRegistry: string;
  envType: EnvironmentType;
  localRepoPath: string;
}
