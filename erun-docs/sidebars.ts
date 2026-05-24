import type { SidebarsConfig } from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  docsSidebar: [
    'intro',
    'why',
    {
      type: 'category',
      label: 'Getting started',
      link: { type: 'generated-index' },
      items: ['getting-started/install', 'getting-started/first-environment'],
    },
    {
      type: 'category',
      label: 'Concepts',
      link: { type: 'generated-index' },
      items: [
        'concepts/tenants-and-environments',
        'concepts/local-vs-non-local',
        'concepts/runtime-pods',
        'concepts/cloud-contexts',
      ],
    },
    {
      type: 'category',
      label: 'CLI reference',
      link: { type: 'generated-index' },
      items: [
        'cli/overview',
        'cli/init',
        'cli/open',
        'cli/list',
        'cli/build',
        'cli/push',
        'cli/deploy',
        'cli/doctor',
        'cli/mcp',
        'cli/release',
        'cli/delete',
        'cli/version',
      ],
    },
    {
      type: 'category',
      label: 'Desktop app',
      link: { type: 'generated-index' },
      items: ['desktop/overview'],
    },
    {
      type: 'category',
      label: 'MCP',
      link: { type: 'generated-index' },
      items: ['mcp/overview'],
    },
    {
      type: 'category',
      label: 'Agent collaboration',
      link: { type: 'generated-index' },
      items: [
        'collaboration/overview',
        'collaboration/operator-in-the-loop',
        'collaboration/reviews',
        'collaboration/comments',
        'collaboration/builds',
      ],
    },
    {
      type: 'category',
      label: 'Deployment',
      link: { type: 'generated-index' },
      items: ['deployment/registries', 'deployment/release-flow'],
    },
    {
      type: 'category',
      label: 'Reference',
      link: { type: 'generated-index' },
      items: ['reference/config-locations', 'reference/env-vars'],
    },
  ],
};

export default sidebars;
