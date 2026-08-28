import playwright from 'eslint-plugin-playwright';
import globals from 'globals';
import tseslint from 'typescript-eslint';

// Playwright lint config. Same type-aware bug-finding baseline as the
// frontend; complexity + max-lines budgets are relaxed because spec files
// describe long user flows. eslint-plugin-playwright catches the common
// flakey-test patterns (await on expect, no .only in committed code).

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
    files: ['tests/**/*.ts', 'fixtures/**/*.ts', 'pages/**/*.ts'],
    ...playwright.configs['flat/recommended'],
    rules: {
      ...playwright.configs['flat/recommended'].rules,
      'playwright/no-skipped-test': 'warn',
      'playwright/expect-expect': [
        'error',
        { assertFunctionNames: ['expect', 'expectDialogContentStaysWithinCard'] },
      ],
    },
  },
);
