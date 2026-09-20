import { describe, expect, it } from 'vitest';

import {
  addClaudeModelId,
  isClaudeModelToken,
  selectableClaudeModelIds,
} from '@/components/app/claudeModels.helpers';

describe('isClaudeModelToken', () => {
  it('accepts every shipped gateway id shape', () => {
    // The ids an operator actually configures: vendor-prefixed with slashes and
    // dots, plus the plain aliases the catalog may also carry.
    for (const id of [
      'deepseek/deepseek-v4.1-flash',
      'anthropic/claude-fable-5.1',
      'openai/gpt-6-astra',
      'google/gemini-3.8-flash',
      'deepseek/deepseek-v4-pro-0813',
      'opus',
    ]) {
      expect(isClaudeModelToken(id)).toBe(true);
    }
  });

  it('rejects anything that would reach the shell as syntax', () => {
    // A value outside the pattern is dropped at launch, so it must be refused
    // at entry rather than saved and silently ignored.
    for (const id of ['a b', 'model; rm -rf /', 'a$(whoami)', 'a|b', 'a&b', 'a`b`', 'a\nb', '']) {
      expect(isClaudeModelToken(id)).toBe(false);
    }
  });

  it('ignores surrounding whitespace', () => {
    expect(isClaudeModelToken('  deepseek/deepseek-v4.1-flash  ')).toBe(true);
  });
});

describe('addClaudeModelId', () => {
  it('appends an id to the entries already selected', () => {
    expect(
      addClaudeModelId({
        base: ['anthropic/claude-fable-5.1'],
        known: ['anthropic/claude-fable-5.1', 'openai/gpt-6-astra'],
        id: 'openai/gpt-6-astra',
      }),
    ).toEqual(['anthropic/claude-fable-5.1', 'openai/gpt-6-astra']);
  });

  it('keeps known entries in their catalog order, not the order they were added', () => {
    expect(
      addClaudeModelId({
        base: ['openai/gpt-6-astra'],
        known: ['anthropic/claude-fable-5.1', 'openai/gpt-6-astra'],
        id: 'anthropic/claude-fable-5.1',
      }),
    ).toEqual(['anthropic/claude-fable-5.1', 'openai/gpt-6-astra']);
  });

  it('places an id outside the known set after the known entries', () => {
    expect(
      addClaudeModelId({
        base: ['anthropic/claude-fable-5.1'],
        known: ['anthropic/claude-fable-5.1'],
        id: 'vendor/typed-by-hand',
      }),
    ).toEqual(['anthropic/claude-fable-5.1', 'vendor/typed-by-hand']);
  });

  it('keeps ids outside the known set in the order they were added', () => {
    expect(
      addClaudeModelId({
        base: ['vendor/first'],
        known: ['anthropic/claude-fable-5.1'],
        id: 'vendor/second',
      }),
    ).toEqual(['vendor/first', 'vendor/second']);
  });

  it('does not duplicate an id that is already selected', () => {
    expect(addClaudeModelId({ base: ['a/b'], known: ['a/b'], id: 'a/b' })).toEqual(['a/b']);
  });

  it('trims the id it adds', () => {
    expect(addClaudeModelId({ base: [], known: [], id: '  a/b  ' })).toEqual(['a/b']);
  });

  it('preserves the selection when adding to an empty known set', () => {
    expect(addClaudeModelId({ base: ['a/b', 'c/d'], known: [], id: 'e/f' })).toEqual([
      'a/b',
      'c/d',
      'e/f',
    ]);
  });
});

describe('selectableClaudeModelIds', () => {
  it('returns usable ids and drops blank or malformed rows', () => {
    // A default model must never be selectable from a row the launch would drop.
    expect(
      selectableClaudeModelIds([
        { id: 'deepseek/deepseek-v4.1-flash' },
        { id: '' },
        { id: '   ' },
        { id: 'a b; rm' },
        { id: 'openai/gpt-6-astra' },
      ]),
    ).toEqual(['deepseek/deepseek-v4.1-flash', 'openai/gpt-6-astra']);
  });

  it('returns nothing for a catalog with no usable row', () => {
    expect(selectableClaudeModelIds([])).toEqual([]);
    expect(selectableClaudeModelIds([{ id: '' }])).toEqual([]);
  });

  it('drops a row declared to require reasoning echo', () => {
    // The declared listing is the one model a default must not name: it is what
    // the environment renders as ANTHROPIC_MODEL and what an exec agent job
    // starts on, and no erun lane can drive it.
    expect(
      selectableClaudeModelIds([
        { id: 'deepseek/deepseek-v4.1-flash', requiresReasoningEcho: true },
        { id: 'openai/gpt-6-astra' },
        { id: 'anthropic/claude-fable-5.1', requiresReasoningEcho: false },
      ]),
    ).toEqual(['openai/gpt-6-astra', 'anthropic/claude-fable-5.1']);
  });

  it('returns nothing when every usable row is declared', () => {
    expect(
      selectableClaudeModelIds([
        { id: 'deepseek/deepseek-v4.1-flash', requiresReasoningEcho: true },
      ]),
    ).toEqual([]);
  });
});
