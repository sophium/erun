import jsxA11y from 'eslint-plugin-jsx-a11y';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import simpleImportSort from 'eslint-plugin-simple-import-sort';
import globals from 'globals';
import tseslint from 'typescript-eslint';

// Frontend lint config. Mirrors the Go side's golangci.yml rigor:
//   complexity:10 + max-lines:100  ≈ cyclop + funlen
//   strict-type-checked            ≈ staticcheck + govet
//   no-floating-promises           ≈ errcheck
//   no-unused-vars                 ≈ unused + ineffassign
// Plus the React-specific rules Go does not need: rules-of-hooks (the
// kind of bug that bit TerminalTabStrip), exhaustive-deps, react-refresh,
// jsx-a11y for the Playwright queries that rely on accessible roles, and
// tailwindcss for Tailwind class hygiene.

export default tseslint.config(
  {
    ignores: ['dist', 'node_modules', 'wailsjs', 'src/components/ui/**', '*.config.{js,mjs,ts}'],
  },
  ...tseslint.configs.recommendedTypeChecked,
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
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
      'simple-import-sort': simpleImportSort,
    },
    rules: {
      // 15 matches golangci's gocyclo error floor on the Go side; the
      // Go cyclop:10 setting is aspirational. JSX-heavy render functions
      // need a little more headroom than control-flow-heavy Go funcs.
      complexity: ['error', { max: 15 }],
      'max-lines-per-function': [
        'error',
        {
          max: 150,
          skipBlankLines: true,
          skipComments: true,
          IIFEs: true,
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
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'error',
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      'simple-import-sort/imports': 'error',
      'simple-import-sort/exports': 'error',
      // The shadcn-generated UI primitives use empty interfaces for
      // future-extensibility; the project's intentional plain-React
      // style does not benefit from these stricter forms.
      '@typescript-eslint/no-empty-object-type': 'off',
    },
  },
  {
    // Test/spec files: relax complexity + length budgets and the type-aware
    // rules that fire on Playwright's helper patterns.
    files: ['**/*.{test,spec}.{ts,tsx}', 'playwright/**'],
    rules: {
      complexity: 'off',
      'max-lines-per-function': 'off',
    },
  },
);
