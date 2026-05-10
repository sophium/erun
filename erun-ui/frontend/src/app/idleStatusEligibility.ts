import type { UIIdleStatus } from '@/types';

export function displayableIdleStatus(status: UIIdleStatus | null): UIIdleStatus | null {
  return isIdleStatusDisplayEligible(status) ? status : null;
}

export function isIdleStatusDisplayEligible(status: UIIdleStatus | null): status is UIIdleStatus {
  if (!status?.managedCloud) {
    return false;
  }
  return (status.cloudContextStatus || '').trim().toLowerCase() === 'running';
}
