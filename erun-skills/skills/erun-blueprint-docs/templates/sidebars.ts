import type { SidebarsConfig } from '@docusaurus/plugin-content-docs';

// File each page id under its audience category, not its topic. The id is the
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
