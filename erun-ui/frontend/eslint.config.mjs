import { builtinRules } from 'eslint/use-at-your-own-risk';
import jsxA11y from 'eslint-plugin-jsx-a11y';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import simpleImportSort from 'eslint-plugin-simple-import-sort';
import globals from 'globals';
import tseslint from 'typescript-eslint';

// Every rule is `error`; inline disables and downgrades are not allowed.
// approaching-max-lines aliases max-lines at 'warn' with a lower cap as an early warning.
const approachingMaxLines = builtinRules.get('max-lines');

export default tseslint.config(
  {
    ignores: ['dist', 'node_modules', 'wailsjs', '*.config.{js,mjs,ts}'],
  },
  ...tseslint.configs.strictTypeChecked,
  ...tseslint.configs.stylisticTypeChecked,
  jsxA11y.flatConfigs.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2022,
      globals: {
        ...globals.browser,
        ...globals.node,
      },
      parser: tseslint.parser,
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
        ecmaFeatures: { jsx: true },
      },
      sourceType: 'module',
    },
    plugins: {
      'lines-budget': { rules: { 'approaching-max-lines': approachingMaxLines } },
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
      'simple-import-sort': simpleImportSort,
    },
    rules: {
      complexity: ['error', { max: 10 }],
      'max-lines-per-function': [
        'error',
        {
          max: 100,
          skipBlankLines: true,
          skipComments: true,
          IIFEs: true,
        },
      ],
      'max-lines': [
        'error',
        {
          max: 500,
          skipBlankLines: true,
          skipComments: true,
        },
      ],
      'lines-budget/approaching-max-lines': [
        'warn',
        {
          max: 450,
          skipBlankLines: true,
          skipComments: true,
        },
      ],
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
        },
      ],
      // WCAG 2.1.1 needs scrollable regions focusable and no ARIA role marks them
      // interactive, so allow tabIndex only on role="region".
      'jsx-a11y/no-noninteractive-tabindex': ['error', { roles: ['region'] }],
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'error',
      'react-refresh/only-export-components': ['error', { allowConstantExport: true }],
      'simple-import-sort/imports': 'error',
      'simple-import-sort/exports': 'error',
    },
  },
  {
    // Type-aware rules misfire on Playwright helper patterns.
    files: ['**/*.{test,spec}.{ts,tsx}', 'playwright/**'],
    rules: {
      complexity: 'off',
      'max-lines-per-function': 'off',
      'max-lines': 'off',
      'lines-budget/approaching-max-lines': 'off',
      // The node:test runner awaits top-level test() calls itself.
      '@typescript-eslint/no-floating-promises': 'off',
    },
  },
);
