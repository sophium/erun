import { themes as prismThemes } from 'prism-react-renderer';
import type { Config } from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const config: Config = {
  title: 'ERun',
  tagline: 'Agentic coding from idea to production — at speed, without compromising compliance.',
  favicon: 'img/favicon.svg',

  url: 'https://docs.erunpaas.com',
  baseUrl: '/',

  organizationName: 'sophium',
  projectName: 'erun',

  onBrokenLinks: 'throw',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  markdown: {
    mermaid: true,
    hooks: {
      onBrokenMarkdownLinks: 'warn',
    },
  },

  themes: ['@docusaurus/theme-mermaid'],

  plugins: [
    [
      require.resolve('@easyops-cn/docusaurus-search-local'),
      {
        hashed: true,
        indexBlog: false,
        docsRouteBasePath: '/',
      },
    ],
  ],

  presets: [
    [
      'classic',
      {
        docs: {
          routeBasePath: '/',
          sidebarPath: './sidebars.ts',
          editUrl: 'https://github.com/sophium/erun/tree/main/erun-docs/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: 'img/erun-social-card.png',
    mermaid: {
      theme: { light: 'neutral', dark: 'dark' },
      options: {
        flowchart: {
          padding: 12,
          nodeSpacing: 36,
          rankSpacing: 36,
          curve: 'basis',
          htmlLabels: true,
        },
        themeVariables: {
          // Brand palette: charcoal text / cyan strokes / tinted fills.
          primaryColor: '#f4f8f7',
          primaryTextColor: '#0f1320',
          primaryBorderColor: '#0891b2',
          secondaryColor: '#e6f1f5',
          tertiaryColor: '#ffffff',
          lineColor: '#0891b2',
          mainBkg: '#f4f8f7',
          edgeLabelBackground: '#ffffff',
          labelColor: '#0f1320',
          // State diagram specifics
          transitionColor: '#0891b2',
          transitionLabelColor: '#0f1320',
          altBackground: '#ffffff',
          // Typography matches the rest of the site
          fontFamily: '-apple-system, BlinkMacSystemFont, "Inter", "Segoe UI", Roboto, sans-serif',
          fontSize: '14px',
        },
      },
    },
    navbar: {
      title: 'ERun',
      logo: {
        alt: 'ERun',
        src: 'img/erun-icon.png',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docsSidebar',
          position: 'left',
          label: 'Docs',
        },
        {
          href: 'https://github.com/sophium/erun',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            { label: 'Getting started', to: '/getting-started/install' },
            { label: 'CLI reference', to: '/cli/overview' },
            { label: 'Concepts', to: '/concepts/tenants-and-environments' },
          ],
        },
        {
          title: 'Project',
          items: [
            { label: 'GitHub', href: 'https://github.com/sophium/erun' },
            { label: 'Issues', href: 'https://github.com/sophium/erun/issues' },
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} Sophium.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'yaml', 'json', 'go', 'docker', 'hcl'],
    },
    colorMode: {
      defaultMode: 'light',
      respectPrefersColorScheme: true,
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
