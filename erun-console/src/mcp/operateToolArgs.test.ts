import { describe, expect, it } from 'vitest';

import { buildOperateArgs, operateFormValid } from './operateToolArgs';

const BASE = {
  tool: 'deploy',
  version: '',
  contextName: '',
  force: false,
  cpu: '',
  memory: '',
  dindCpu: '',
  dindMemory: '',
  applyRecommendation: false,
  preview: true,
};

describe('buildOperateArgs', () => {
  it('sends only version (trimmed) for deploy, alongside preview', () => {
    expect(buildOperateArgs({ ...BASE, tool: 'deploy', version: ' 1.2.3 ' })).toEqual({
      preview: true,
      version: '1.2.3',
    });
  });

  it('sends name and force for context_start', () => {
    expect(
      buildOperateArgs({ ...BASE, tool: 'context_start', contextName: 'ctx1', force: true }),
    ).toEqual({ preview: true, name: 'ctx1', force: true });
  });

  it('sends only name for context_stop -- force is not part of its schema', () => {
    expect(
      buildOperateArgs({ ...BASE, tool: 'context_stop', contextName: 'ctx1', force: true }),
    ).toEqual({ preview: true, name: 'ctx1' });
  });

  it('omits blank resize dimensions rather than sending empty strings', () => {
    expect(
      buildOperateArgs({ ...BASE, tool: 'resize', cpu: '6', applyRecommendation: false }),
    ).toEqual({ preview: true, cpu: '6', applyRecommendation: false });
  });

  it('sends applyRecommendation with no explicit dimensions', () => {
    expect(buildOperateArgs({ ...BASE, tool: 'resize', applyRecommendation: true })).toEqual({
      preview: true,
      applyRecommendation: true,
    });
  });
});

describe('operateFormValid', () => {
  it('requires a version for deploy', () => {
    expect(operateFormValid({ tool: 'deploy', version: '', contextName: '' })).toBe(false);
    expect(operateFormValid({ tool: 'deploy', version: '1.2.3', contextName: '' })).toBe(true);
  });

  it('requires a name for both context tools', () => {
    expect(operateFormValid({ tool: 'context_start', version: '', contextName: '' })).toBe(false);
    expect(operateFormValid({ tool: 'context_stop', version: '', contextName: 'ctx1' })).toBe(true);
  });

  it('has no required field for resize', () => {
    expect(operateFormValid({ tool: 'resize', version: '', contextName: '' })).toBe(true);
  });
});
