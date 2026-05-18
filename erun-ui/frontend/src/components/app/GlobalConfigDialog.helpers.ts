export const NOT_CONFIGURED_VALUE = '__none__';

export const dialogErrorClassName =
  'rounded-[var(--radius)] border border-[color-mix(in_oklch,var(--destructive)_36%,transparent)] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] px-[11px] py-[9px] text-[13px] leading-[1.35] text-destructive [overflow-wrap:anywhere]';

const AWS_REGION_NAMES: Record<string, string> = {
  'eu-west-1': 'Ireland',
  'eu-west-2': 'London',
  'eu-west-3': 'Paris',
  'eu-central-1': 'Frankfurt',
  'eu-north-1': 'Stockholm',
  'eu-south-1': 'Milan',
  'us-east-1': 'N. Virginia',
  'us-east-2': 'Ohio',
  'us-west-1': 'N. California',
  'us-west-2': 'Oregon',
  'ap-northeast-1': 'Tokyo',
  'ap-northeast-2': 'Seoul',
  'ap-south-1': 'Mumbai',
  'ap-southeast-1': 'Singapore',
  'ap-southeast-2': 'Sydney',
};

export function cloudRegionLabel(region: string): string {
  const name = AWS_REGION_NAMES[region];
  return name ? `${region} (${name})` : region;
}

export function cloudProviderSummary(provider: {
  provider: string;
  username?: string;
  accountId?: string;
}): string {
  const providerName = provider.provider.toUpperCase();
  if (provider.accountId && provider.username) {
    return `${providerName} account ${provider.accountId} - ${provider.username}`;
  }
  if (provider.accountId) {
    return `${providerName} account ${provider.accountId}`;
  }
  return providerName;
}

export function cloudContextSummary(context: {
  cloudProviderAlias: string;
  region: string;
  instanceType: string;
  diskSizeGb: number;
  diskType: string;
  instanceId?: string;
}): string {
  const parts = [
    context.cloudProviderAlias,
    cloudRegionLabel(context.region),
    context.instanceType,
    `${String(context.diskSizeGb)} GB ${context.diskType}`,
  ].filter(Boolean);
  if (context.instanceId) {
    parts.push(context.instanceId);
  }
  return parts.join(' - ');
}

export function generatedContextName(
  provider: { alias: string; username?: string; accountId?: string } | undefined,
  region: string,
  contexts: { name: string; kubernetesContext: string }[],
): string {
  if (!provider) {
    return '';
  }
  const identity =
    (provider.accountId?.trim() ? provider.accountId : '') ||
    (provider.username?.trim() ? provider.username : '') ||
    provider.alias;
  const tail = sanitizeContextName([identity, region || 'eu-west-2'].filter(Boolean).join('-'));
  return nextGeneratedContextName(tail, contexts);
}

function nextGeneratedContextName(
  tail: string,
  contexts: { name: string; kubernetesContext: string }[],
): string {
  const normalizedTail = sanitizeContextName(tail) || 'context';
  const suffix = `-${normalizedTail}`;
  let next = 1;
  for (const context of contexts) {
    for (const name of [context.name, context.kubernetesContext]) {
      if (!name.startsWith('erun-') || !name.endsWith(suffix)) {
        continue;
      }
      const counter = name.slice('erun-'.length, name.length - suffix.length);
      if (!/^\d{3}$/.test(counter)) {
        continue;
      }
      const value = Number(counter);
      if (value >= next) {
        next = value + 1;
      }
    }
  }
  return `erun-${String(next).padStart(3, '0')}-${normalizedTail}`;
}

function sanitizeContextName(value: string): string {
  let result = '';
  let lastDash = false;
  for (const char of value.trim().toLowerCase()) {
    if ((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')) {
      result += char;
      lastDash = false;
      continue;
    }
    if (!lastDash) {
      result += '-';
      lastDash = true;
    }
  }
  return result.replace(/^-+|-+$/g, '');
}

export function statusLabel(status: string): string {
  switch (status) {
    case 'active':
      return 'Active';
    case 'running':
      return 'Running';
    case 'stopped':
      return 'Stopped';
    case 'pending':
      return 'Pending';
    case 'expired':
      return 'Expired';
    case 'not_configured':
      return 'Not configured';
    default:
      return 'Unknown';
  }
}

export function optionValues(values: string[], current: string): string[] {
  const seen = new Set<string>();
  return [current, ...values]
    .map((value) => value.trim())
    .filter((value) => {
      if (!value || seen.has(value)) {
        return false;
      }
      seen.add(value);
      return true;
    });
}
