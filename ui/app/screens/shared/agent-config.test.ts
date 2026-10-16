import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { AgentConfigFinding } from '../../api/types.ts';
import { AGENT_CONFIG_WHY, findingKey, trustKey, untrusted, untrustedSummary } from './agent-config.ts';

const finding = (over: Partial<AgentConfigFinding> = {}): AgentConfigFinding => ({
  adapter: 'gemini',
  where: 'the repository',
  taskId: '',
  files: ['.gemini/settings.json'],
  fingerprint: 'a'.repeat(64),
  trusted: false,
  ...over,
});

describe('agent configuration helpers (M3)', () => {
  it('explain in two sentences why this matters: the files can run programs or send prompts, and Drydock does not start the CLI there', () => {
    expect(AGENT_CONFIG_WHY.split(/(?<=\.) (?=[A-Z])/)).toHaveLength(2);
    expect(AGENT_CONFIG_WHY).toContain('run programs or send prompts elsewhere');
    expect(AGENT_CONFIG_WHY).toContain('Drydock does not start the CLI');
    expect(AGENT_CONFIG_WHY).toContain('not trusted');
  });

  it('pick out the findings nobody trusted', () => {
    const list = [finding(), finding({ adapter: 'opencode', trusted: true }), finding({ where: 'the worktree of task 3, Fix login', taskId: 'tsk_3' })];
    expect(untrusted(list).map((f) => f.where)).toEqual(['the repository', 'the worktree of task 3, Fix login']);
    expect(untrusted([])).toEqual([]);
  });

  it('tell one finding from another by CLI, place and state, and trust by CLI and state', () => {
    const a = finding();
    const b = finding({ taskId: 'tsk_3' });
    const c = finding({ fingerprint: 'b'.repeat(64) });
    expect(new Set([findingKey(a), findingKey(b), findingKey(c)]).size).toBe(3);
    // the same files in two places are trusted with one click: they share the key trusting uses
    expect(trustKey(a)).toBe(trustKey(b));
    expect(trustKey(a)).not.toBe(trustKey(c));
  });

  it('summarise how many places need a decision and for which CLIs', () => {
    expect(untrustedSummary([finding()])).toBe('1 place has configuration that an agent CLI would act on and nobody trusted (gemini)');
    expect(untrustedSummary([finding(), finding({ adapter: 'opencode' }), finding({ adapter: 'opencode', trusted: true })])).toBe(
      '2 places have configuration that agent CLIs would act on and nobody trusted (gemini, opencode)',
    );
  });
});
