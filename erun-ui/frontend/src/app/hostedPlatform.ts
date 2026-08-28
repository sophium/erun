// erun's own hosted platform is a single apex host serving every tenant —
// never a per-tenant or per-environment one (the `services.<domain>` family
// is the per-environment exposure edge, a different thing entirely). Keep in
// sync with erun-common's HostedPlatformAPIURL, the CLI's copy of this same
// literal; the two cannot import each other directly, so this comment is the
// tripwire against drift.
export const HOSTED_PLATFORM_API_URL = 'https://api.erunpaas.com';
