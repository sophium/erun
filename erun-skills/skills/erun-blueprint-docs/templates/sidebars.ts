import type { SidebarsConfig } from '@docusaurus/plugin-content-docs';

// Blueprint sidebar. Add each new page id under the category that matches its
// audience (Operator-facing vs Agent-reference), not its topic. The id is the
// path relative to docs/ without the .md extension.
const sidebars: SidebarsConfig = {
  docsSidebar: [
    'intro',
    {
      type: 'category',
      label: 'Getting started',
      link: { type: 'generated-index' },
      items: [],
    },
    {
      type: 'category',
      label: 'Reference',
      link: { type: 'generated-index' },
      items: [],
    },
  ],
};

export default sidebars;
