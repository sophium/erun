import type { UIExposedService } from '@/uiExposureTypes';

// exposedServiceStatus turns an exposed service's raw scheme/tlsReady fields
// into the three states a developer actually needs to tell apart when
// deciding whether a URL is safe to open: no TLS was ever configured for it,
// TLS is configured but the certificate has not issued yet (opening it now
// would show a warning, not a working page), and TLS is configured and the
// certificate is actually Ready. Kept pure and separate from the row
// component so the derivation is testable without rendering the Ports tab.
export type ExposedServiceStatus = 'http' | 'https-pending' | 'https-ready';

export function exposedServiceStatus(service: UIExposedService): ExposedServiceStatus {
  if (service.scheme !== 'https') {
    return 'http';
  }
  return service.tlsReady ? 'https-ready' : 'https-pending';
}
