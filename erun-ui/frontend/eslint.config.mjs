import jsxA11y from 'eslint-plugin-jsx-a11y';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import simpleImportSort from 'eslint-plugin-simple-import-sort';
import globals from 'globals';
import tseslint from 'typescript-eslint';

// Frontend lint config. Mirrors the Go side's golangci.yml rigor and goes
// further: cyclop:10 + funlen:100/50 on the Go side ⇒ complexity:10 +
// max-lines-per-function:100 here; staticcheck + govet + errcheck ⇒
// strictTypeChecked + stylisticTypeChecked. Every rule here is `error`;
// inline disables and rule downgrades are not allowed (see
// ~/.claude/projects/<this-repo>/memory/feedback_lint_no_disable.md).

export default tseslint.config(
  {
    ignores: ['dist', 'node_modules', 'wailsjs', 'src/components/ui/**', '*.config.{js,mjs,ts}'],
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
      // Per-file size complements the per-function budget: a file of
      // many small functions is fine; a 1500-line module is not.
      'max-lines': [
        'error',
        {
          max: 500,
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
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'error',
      'react-refresh/only-export-components': ['error', { allowConstantExport: true }],
      'simple-import-sort/imports': 'error',
      'simple-import-sort/exports': 'error',
    },
  },
  {
    // Test/spec files: relax complexity + length budgets and the type-aware
    // rules that fire on Playwright's helper patterns.
    files: ['**/*.{test,spec}.{ts,tsx}', 'playwright/**'],
    rules: {
      complexity: 'off',
      'max-lines-per-function': 'off',
      'max-lines': 'off',
    },
  },
);
