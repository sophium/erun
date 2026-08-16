import playwright from 'eslint-plugin-playwright';
import globals from 'globals';
import tseslint from 'typescript-eslint';

// Lint config for the console's Playwright e2e. Mirrors erun-ui/playwright's
// config: the same type-aware bug-finding baseline, with eslint-plugin-playwright
// catching the common flaky-test patterns (await on expect, no .only committed).

export default tseslint.config(
  {
    ignores: [
      'node_modules',
      'playwright-report',
      'test-results',
      '.cache',
      '*.config.{js,mjs,ts}',
    ],
  },
  ...tseslint.configs.recommendedTypeChecked,
  {
    files: ['**/*.ts'],
    languageOptions: {
      ecmaVersion: 2022,
      globals: {
        ...globals.node,
      },
      parser: tseslint.parser,
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
      sourceType: 'module',
    },
    rules: {
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
        },
      ],
    },
  },
  {
    files: ['tests/**/*.ts'],
    ...playwright.configs['flat/recommended'],
    rules: {
      ...playwright.configs['flat/recommended'].rules,
      // The suite is opt-in behind ERUN_E2E_CONSOLE_OIDC, which is a conditional
      // skip — the rule's own supported shape, not a disabled test.
      'playwright/no-skipped-test': ['error', { allowConditional: true }],
    },
  },
);
