import playwright from 'eslint-plugin-playwright';
import globals from 'globals';
import tseslint from 'typescript-eslint';

// The same type-aware baseline as the console's own Playwright package, with
// eslint-plugin-playwright catching the common flaky-test patterns.

export default tseslint.config(
  {
    ignores: ['node_modules', 'playwright-report', 'test-results', '*.config.{js,mjs,ts}'],
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
      // Suites that need a credential skip, naming it, when it is not supplied:
      // a conditional skip, the rule's own supported shape.
      'playwright/no-skipped-test': ['error', { allowConditional: true }],
    },
  },
);
