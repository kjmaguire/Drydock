import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { money } from '../screens/shared/explain.tsx';
import { priceText } from '../screens/providers/providers.ts';
import { agentStatus, agoWords, formatBytes, formatCost, formatUsd, presenceWords } from './format.ts';
import type { PresenceState } from '../api/types.ts';

describe('agoWords', () => {
  it('says how long ago a time was in plain words, the one wording every page uses', () => {
    const now = Date.UTC(2026, 9, 24, 12);
    expect(agoWords(now - 20_000, now)).toBe('just now');
    expect(agoWords(now - 2 * 60_000, now)).toBe('2 min ago');
    expect(agoWords(now - 3 * 3_600_000, now)).toBe('3 h ago');
    expect(agoWords(now - 49 * 3_600_000, now)).toBe('2 d ago');
    expect(agoWords(now + 60_000, now)).toBe('just now');
    expect(agoWords(0, now)).toBe('');
  });
});

describe('formatBytes', () => {
  it('rounds before choosing the unit, so a unit boundary never prints 1024', () => {
    expect(formatBytes(1_048_500)).toBe('1.0 MB');
    expect(formatBytes(1023)).toBe('1023 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(10 * 1024)).toBe('10 KB');
    expect(formatBytes(1024 * 1024 - 1)).toBe('1.0 MB');
    expect(formatBytes(5 * 1024 ** 3)).toBe('5.0 GB');
    expect(formatBytes(undefined)).toBe('unknown');
  });
});

describe('formatUsd', () => {
  it('never shows an amount above zero as $0, and every screen uses the same rule', () => {
    expect(formatUsd(0.00004)).toBe('<$0.0001');
    expect(formatUsd(0.00005)).toBe('$0.0001');
    expect(formatUsd(0.0002)).toBe('$0.0002');
    expect(formatUsd(0.0123)).toBe('$0.01');
    expect(formatUsd(1.5)).toBe('$1.50');
    expect(formatUsd(0)).toBe('$0.00');
    // the routing estimate, the task cost and the provider price follow it (zero alike)
    expect(money(0)).toBe('$0.00');
    expect(money(0.00004)).toBe('<$0.0001');
    expect(money(null)).toBe('price unknown');
    expect(formatCost(0.00004)).toBe('<$0.0001');
    const m = { isFree: false, inputPerM: 0.00001, outputPerM: 0 } as Parameters<typeof priceText>[0];
    expect(priceText(m, 'in')).toBe('<$0.0001');
  });
});

describe('agent status (P86)', () => {
  const p = (over: Partial<PresenceState>): PresenceState => ({ agentId: 'a', presence: 'Idle', since: '', updated: '', seq: 1, ...over });
  it('maps every presence of the core to one of a few words, and never says Unknown', () => {
    const cases: Array<[PresenceState | undefined, string, string]> = [
      [undefined, 'stopped', 'Stopped'],
      [p({ presence: 'Paused', label: 'stopped' }), 'stopped', 'Stopped'],
      [p({ presence: 'Working', activity: 'tool: Read' }), 'working', 'Working'],
      [p({ presence: 'Thinking' }), 'working', 'Working'],
      [p({ presence: 'Needs Attention', label: 'watchdog' }), 'waiting-for-you', 'Waiting for you'],
      [p({ presence: 'Paused', label: 'budget' }), 'waiting-for-you', 'Waiting for you'],
      [p({ presence: 'Waiting', activity: 'awaiting permission' }), 'waiting-for-you', 'Waiting for you'],
      [p({ presence: 'Waiting', activity: 'waiting' }), 'waiting', 'Waiting'],
      [p({ presence: 'Idle', label: 'starting' }), 'starting', 'Starting'],
      [p({ presence: 'Idle' }), 'idle', 'Idle'],
      [p({ presence: 'Completed', activity: 'turn finished' }), 'idle', 'Idle'],
      [p({ presence: 'Failed', activity: 'exit 1' }), 'error', 'Error'],
      [p({ presence: 'Something new' }), 'idle', 'Idle'],
    ];
    for (const [pr, status, words] of cases) {
      expect(agentStatus(pr)).toBe(status);
      expect(presenceWords(pr)).toBe(words);
    }
  });
});
