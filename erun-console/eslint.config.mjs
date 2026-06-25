import jsxA11y from 'eslint-plugin-jsx-a11y';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import simpleImportSort from 'eslint-plugin-simple-import-sort';
import globals from 'globals';
import tseslint from 'typescript-eslint';

// Lint config for erun-console. Mirrors erun-ui/frontend/eslint.config.mjs so the
// hosted SPA and the desktop frontend share one rigor bar: type-aware
// typescript-eslint (strictTypeChecked + stylisticTypeChecked), complexity:10,
// max-lines-per-function:100, max-lines:500, React hooks/refresh rules, and
// jsx-a11y. Every rule is `error`; inline disables and rule downgrades are not
// allowed (see root AGENTS.md: fix lint findings by correcting the code).

export default tseslint.config(
  {
    // `playwright/` is a separate yarn package (the OIDC e2e) with its own
    // eslint/tsconfig — like erun-ui/frontend vs erun-ui/playwright — so the
    // app's type-aware lint must not try to parse it against the app tsconfig.
    ignores: ['dist', 'node_modules', 'playwright', '*.config.{js,mjs,ts}'],
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
    // Test/spec + setup files: relax the complexity + length budgets and the
    // type-aware rules that fire on Testing Library / mocking patterns.
    files: ['**/*.{test,spec}.{ts,tsx}', 'src/test/**'],
    rules: {
      complexity: 'off',
      'max-lines-per-function': 'off',
      'max-lines': 'off',
      '@typescript-eslint/no-unsafe-assignment': 'off',
      '@typescript-eslint/no-unsafe-member-access': 'off',
      '@typescript-eslint/no-unsafe-argument': 'off',
      '@typescript-eslint/no-unsafe-call': 'off',
      '@typescript-eslint/no-unsafe-return': 'off',
    },
  },
);
