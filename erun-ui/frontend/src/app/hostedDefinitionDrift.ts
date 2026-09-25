import type { UIHostedDefinitionDrift, UIHostedDefinitionUpload } from '@/uiHostedDefinitionTypes';

import { CheckHostedDefinitionDrift, UploadHostedDefinition } from '../../wailsjs/go/main/App';

// readHostedDefinitionDrift asks the Go side whether the platform's copy of
// this environment has moved past what this machine last pulled. It is a
// read-only call: the Go side compares two revisions and writes nothing, so
// calling it repeatedly is safe, and it is only ever called on an operator's
// own action (the panel's refresh button), never from a poll.
export async function readHostedDefinitionDrift(
  tenant: string,
  environment: string,
): Promise<UIHostedDefinitionDrift> {
  return await CheckHostedDefinitionDrift(tenant, environment);
}

// uploadHostedDefinition sends this environment's portable settings to the
// platform row its marker names. The desktop also does this on its own when a
// config change is not its own write; this is the operator's path, and it is
// the recovery action the panel offers when the automatic one could not reach
// the platform.
export async function uploadHostedDefinition(
  tenant: string,
  environment: string,
): Promise<UIHostedDefinitionUpload> {
  return await UploadHostedDefinition(tenant, environment);
}
