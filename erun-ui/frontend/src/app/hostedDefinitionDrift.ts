import type { UIHostedDefinitionDrift, UIHostedDefinitionUpload } from '@/uiHostedDefinitionTypes';

import { CheckHostedDefinitionDrift, UploadHostedDefinition } from '../../wailsjs/go/main/App';

// readHostedDefinitionDrift is read-only and runs only on operator action, never
// from a poll.
export async function readHostedDefinitionDrift(
  tenant: string,
  environment: string,
): Promise<UIHostedDefinitionDrift> {
  return await CheckHostedDefinitionDrift(tenant, environment);
}

export async function uploadHostedDefinition(
  tenant: string,
  environment: string,
): Promise<UIHostedDefinitionUpload> {
  return await UploadHostedDefinition(tenant, environment);
}
