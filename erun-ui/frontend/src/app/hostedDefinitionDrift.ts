import type { UIHostedDefinitionDrift } from '@/uiHostedDefinitionTypes';

import { CheckHostedDefinitionDrift } from '../../wailsjs/go/main/App';

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
