import type { UIExposedService } from '@/uiExposureTypes';

// ExposedServiceStatus separates https whose certificate has not issued yet from
// https that is ready.
export type ExposedServiceStatus = 'http' | 'https-pending' | 'https-ready';

export function exposedServiceStatus(service: UIExposedService): ExposedServiceStatus {
  if (service.scheme !== 'https') {
    return 'http';
  }
  return service.tlsReady ? 'https-ready' : 'https-pending';
}
