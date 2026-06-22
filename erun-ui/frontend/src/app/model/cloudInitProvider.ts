// CloudInitProvider identifies which provider a guided `erun cloud init`
// terminal session is setting up, so exit toasts name the right provider
// instead of always saying "AWS".
export type CloudInitProvider = 'aws' | 'cloudflare';
