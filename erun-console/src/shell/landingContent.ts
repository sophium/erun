import { Columns2, Gauge, Layers, ShieldCheck } from 'lucide-react';

// Bundled product-level defaults for the signed-out landing page. Mirrors how
// BrandMark already falls back to a generic mark when an instance sets no
// brand (#1327): an instance that configures no tagline still gets a real
// pitch instead of a blank hero, while one that does configure `tagline`
// (GET /v1/platform) overrides this text entirely.
export const DEFAULT_TAGLINE =
  'Agentic coding from idea to production — at speed, without compromising compliance.';

export const DEFAULT_DESCRIPTION =
  'Every task — a feature, a peer review, a hotfix — gets its own isolated environment with the full stack inside, so an Operator and an AI Agent can work side by side without fighting over one shared dev stack.';

export interface Differentiator {
  key: string;
  label: string;
  description: string;
  icon: typeof Columns2;
}

// The four differentiators from erun-docs/docs/intro.md's "What makes ERun
// different" section, restated as short landing-page copy rather than the
// verbose diagram alt text that page uses.
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
