import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Provider } from '../../api/types.ts';
import {
  actionChips,
  ceilingWords,
  cheapProviders,
  LEVELS,
  levelSentence,
  levelWords,
  minutesOf,
  newRule,
  parseChain,
  parseMinutes,
  parseThen,
  projectLevelOptions,
  RUN_KINDS,
  taskLevelOptions,
  taskLevelSentence,
  runKindLabel,
  runKindOptions,
  sortRules,
  splitChain,
  withChain,
} from './routing.ts';

const rule = (id: string, priority: number) => ({ ...newRule(), id, priority });

describe('routing helpers', () => {
  it('offers every agent turn kind a rule can match (P63) with its words, and leaves other kinds as they are', () => {
    for (const k of ['agent_task', 'review', 'room_reply', 'spec', 'tests', 'design', 'security_review', 'lead', 'recommend']) expect(RUN_KINDS).toContain(k);
    expect(runKindLabel('lead')).toBe('team lead');
    expect(runKindLabel('recommend')).toBe('recommendation');
    expect(runKindLabel('room_reply')).toBe('room reply');
    expect(runKindLabel('security_review')).toBe('security review');
    expect(runKindLabel('consult')).toBe('consult');
    expect(runKindOptions().find(([k]) => k === 'spec')).toEqual(['spec', 'spec (pm) (spec)']);
    expect(runKindOptions().find(([k]) => k === 'classify')).toEqual(['classify', 'classify']);
  });

  it('orders rules by priority, highest first, then by id', () => {
    expect(sortRules([rule('b', 1), rule('a', 5), rule('c', 5)]).map((r) => r.id)).toEqual(['a', 'c', 'b']);
  });

  it('refuses an action that is not JSON with a reason', () => {
    expect(parseThen('{"prefer_tier":"cheap"}')).toEqual({ ok: true, value: { prefer_tier: 'cheap' } });
    const bad = parseThen('{nope');
    expect(bad.ok).toBe(false);
    if (!bad.ok) expect(bad.error).toMatch(/^The action is not valid JSON: /);
  });

  it('turns an action into readable chips', () => {
    expect(actionChips({ prefer_tier: 'cheap', deny: true, require_capability: ['tools', 'vision'], handoff: false, penalty: 2 })).toEqual([
      { key: 'prefer_tier', text: 'prefer tier: cheap', tone: 'accent' },
      { key: 'deny', text: 'deny', tone: 'err' },
      { key: 'require_capability', text: 'require capability: tools, vision', tone: 'muted' },
      { key: 'penalty', text: 'penalty: 2', tone: 'warn' },
    ]);
    expect(actionChips({})).toEqual([]);
    expect(actionChips(null)).toEqual([]);
  });

  it('reads a fallback chain in order, each model once, and keeps it apart from the other actions', () => {
    expect(parseChain(' sonnet, haiku-b  sonnet,,')).toEqual(['sonnet', 'haiku-b']);
    expect(parseChain('')).toEqual([]);
    expect(splitChain({ prefer_tier: 'cheap', fallback: ['a', 'b'] })).toEqual({ rest: { prefer_tier: 'cheap' }, chain: 'a, b' });
    expect(splitChain(null)).toEqual({ rest: {}, chain: '' });
    expect(withChain({ prefer_tier: 'cheap', fallback: ['old'] }, ['a', 'b'])).toEqual({ prefer_tier: 'cheap', fallback: ['a', 'b'] });
    expect(withChain({ fallback: ['old'] }, [])).toEqual({});
    expect(actionChips({ fallback: ['a', 'b'] })).toEqual([{ key: 'fallback', text: 'fallback: a → b', tone: 'muted' }]);
  });

  it('reads a wall-time limit in whole minutes, empty for none, and says it in words (P112)', () => {
    expect(parseMinutes('')).toEqual({ ok: true, seconds: 0 });
    expect(parseMinutes(' 30 ')).toEqual({ ok: true, seconds: 1800 });
    for (const bad of ['0', '1441', '2.5', 'ten', '-3']) expect(parseMinutes(bad).ok).toBe(false);
    expect(minutesOf(1800)).toBe('30');
    expect(minutesOf(undefined)).toBe('');
    expect(minutesOf(0)).toBe('');
    expect([ceilingWords(30), ceilingWords(2700), ceilingWords(7200), ceilingWords(5400)]).toEqual(['30 s', '45 min', '2 h', '1 h 30 min']);
  });

  it('cheap selection covers cheap, private providers only', () => {
    const p = (id: string, tier: string, dataClass: string) => ({ id, tier, dataClass }) as Provider;
    expect(cheapProviders([p('a', 'cheap', 'private'), p('b', 'cheap', 'trainable'), p('c', 'premium', 'private')]).map((x) => x.id)).toEqual(['a']);
  });
});

describe('routing levels (P118)', () => {
  it("a project's choices follow the workspace first, naming its level, then every level with what it does", () => {
    const o = projectLevelOptions('quality');
    expect(o[0]).toEqual({ value: '', label: 'Follow the workspace (Quality)', hint: 'set on the Routing page' });
    expect(o.slice(1).map((x) => x.value)).toEqual(LEVELS);
    expect(o.every((x) => !!x.hint)).toBe(true);
    // a stored level the list does not offer stays choosable
    expect(projectLevelOptions('balanced', 'custom').map((x) => x.value)).toContain('custom');
    expect(levelWords('maximum_quality')).toBe('Maximum quality');
    expect(levelWords('turbo')).toBe('turbo');
  });

  it('says where the level comes from', () => {
    expect(levelSentence({ effective: 'quality', source: 'project' })).toBe("This project's agent turns run at Quality, its own level.");
    expect(levelSentence({ effective: 'balanced', source: 'workspace' })).toBe("This project's agent turns run at Balanced, the workspace's level.");
  });

  it("offers a task's own level after what it follows, and says where a task's level comes from (P127)", () => {
    const opts = taskLevelOptions({ workspace: 'balanced', project: 'cheapest', template: 'Security', templateLevel: 'quality' });
    expect(opts[0]).toEqual({ value: '', label: 'Follow the Security template (Quality)' });
    expect(opts.map((o) => o.value)).toEqual(['', ...LEVELS]);
    expect(taskLevelOptions({ workspace: 'balanced', project: 'cheapest' })[0]!.label).toBe('Follow the project (Cheapest)');
    expect(taskLevelOptions({ workspace: 'fast', project: '' })[0]!.label).toBe('Follow the workspace (Fast)');
    expect(taskLevelOptions({ workspace: 'fast', project: '', task: 'custom' }).map((o) => o.value)).toContain('custom');
    expect(taskLevelSentence({ effective: 'fast', source: 'task' })).toBe("This task's agent turns run at Fast, its own level.");
    expect(taskLevelSentence({ effective: 'quality', source: 'template', template: 'Security' })).toBe("This task's agent turns run at Quality, the level the Security template declares.");
    expect(taskLevelSentence({ effective: 'cheapest', source: 'project' })).toBe("This task's agent turns run at Cheapest, the project's level.");
    expect(taskLevelSentence({ effective: 'balanced', source: 'workspace' })).toBe("This task's agent turns run at Balanced, the workspace's level.");
  });
});
