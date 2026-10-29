import playwright from 'eslint-plugin-playwright';
import globals from 'globals';
import tseslint from 'typescript-eslint';

// Spec files describe long user flows, so complexity and size budgets are relaxed.

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
