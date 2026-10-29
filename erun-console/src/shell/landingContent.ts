import { Columns2, Gauge, Layers, ShieldCheck } from 'lucide-react';

// Default tagline when an instance configures none.
export const DEFAULT_TAGLINE =
  'Agentic coding from idea to production, without compromising compliance.';

export const DEFAULT_DESCRIPTION =
  'Every task — a feature, a peer review, a hotfix — gets its own isolated environment with the full stack inside, so an Operator and an AI Agent can work side by side without fighting over one shared dev stack.';

// Mirrors erun-ui's ERUN_DOCS_URL; used when an instance sets no docsUrl.
export const PUBLIC_DOCS_URL = 'https://docs.erunpaas.com';

export const CONFIGURE_OIDC_DOCS_PATH = '/deployment/deploy-platform#hosted-idp';

export interface Differentiator {
  key: string;
  label: string;
  description: string;
  icon: typeof Columns2;
}

// From erun-docs/docs/intro.md's "What makes ERun different".
export const DIFFERENTIATORS: Differentiator[] = [
  {
    key: 'side-by-side',
    label: 'Side by side',
    description: 'Your editor and your Agent see the same project — no parallel worlds.',
    icon: Columns2,
  },
  {
    key: 'in-control',
    label: 'In control',
    description:
      'Preview every action before it runs. Every action recorded. Join, take over, or hand off any time.',
    icon: Gauge,
  },
  {
    key: 'industry-standards',
    label: 'Industry standards',
    description:
      'Traceable, reproducible, audit-ready — compliance is how the platform works, not a bolt-on.',
    icon: ShieldCheck,
  },
  {
    key: 'your-scale',
    label: 'Your scale',
    description:
      'From a single VM to an autoscaling fleet with DR and immutable backups — the same defaults at every scale.',
    icon: Layers,
  },
];
