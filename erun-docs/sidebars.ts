import type { SidebarsConfig } from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  docsSidebar: [
    'intro',
    {
      type: 'category',
      label: 'Getting started',
      link: { type: 'generated-index' },
      items: [
        'getting-started/install',
        'getting-started/first-environment',
        'getting-started/build-an-app',
        'getting-started/three-scenarios',
      ],
    },
    {
      type: 'category',
      label: 'CLI',
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
      label: 'Operator + Agent',
      link: { type: 'generated-index' },
      items: [
        'collaboration/overview',
        'collaboration/operator-in-the-loop',
        'collaboration/operator-maturity',
        'collaboration/workflow',
      ],
    },
    {
      type: 'category',
      label: 'Operator reference',
      link: { type: 'generated-index' },
      items: [
        'reference/cheatsheet',
        'reference/faq',
        'reference/troubleshooting',
      ],
    },
    {
      type: 'category',
      label: 'Agent reference',
      link: { type: 'generated-index' },
      items: [
        'agent-reference/overview',
        {
          type: 'category',
          label: 'Concepts',
          link: { type: 'generated-index' },
          items: [
            'concepts/glossary',
            'concepts/tenants-and-environments',
            'concepts/environment-types',
            'concepts/runtime-pods',
            'concepts/networking',
            'concepts/observability',
            'concepts/security',
            'concepts/conventions',
            'concepts/skills',
            'concepts/cloud-contexts',
          ],
        },
        {
          type: 'doc',
          id: 'mcp/overview',
          label: 'MCP protocol + tools',
        },
        {
          type: 'doc',
          id: 'collaboration/agent-patterns',
          label: 'Agent patterns',
        },
        {
          type: 'category',
          label: 'erun API',
          link: { type: 'generated-index' },
          items: [
            'agent-reference/api-protocol',
            'agent-reference/audit-log',
            'collaboration/reviews',
            'collaboration/comments',
            'collaboration/builds',
          ],
        },
        {
          type: 'category',
          label: 'Platform spec',
          link: { type: 'generated-index' },
          items: [
            'agent-reference/conventions-spec',
            'agent-reference/idle-policy',
            'agent-reference/cli-flags',
            'agent-reference/dry-run-redaction',
            'agent-reference/release-policy',
            'agent-reference/networking-spec',
            'agent-reference/metrics-spec',
            'agent-reference/workspace-sync-spec',
            'agent-reference/skills-spec',
          ],
        },
        {
          type: 'category',
          label: 'Configuration spec',
          link: { type: 'generated-index' },
          items: [
            'reference/configuration',
            'reference/configuration-build-paths',
            'reference/config-locations',
            'reference/env-vars',
          ],
        },
      ],
    },
    {
      type: 'category',
      label: 'Admin',
      link: { type: 'generated-index' },
      items: [
        'deployment/registries',
        'deployment/release-flow',
        'deployment/cloud-setup',
      ],
    },
  ],
};

export default sidebars;
