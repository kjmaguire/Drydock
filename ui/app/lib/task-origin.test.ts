import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { LEAD_ORIGIN_LABEL, LEAD_ORIGIN_WAITS, leadStep, leadStepText } from './task-origin.ts';

// audit 2026-10-16, M27: a task the team lead split off carries origin "lead:N"; nothing else counts as one.

describe('leadStep', () => {
  it('reads N of "lead:N", how many steps the task is from a task a person made', () => {
    expect(leadStep('lead:1')).toBe(1);
    expect(leadStep('lead:2')).toBe(2);
    expect(leadStep('lead:12')).toBe(12);
    expect(leadStep('lead:999')).toBe(999);
  });

  it('is no origin for a task a person made: absent, null or empty', () => {
    expect(leadStep(undefined)).toBeUndefined();
    expect(leadStep(null)).toBeUndefined();
    expect(leadStep('')).toBeUndefined();
  });

  it('is no origin for anything the core would not send: not a string, or any other text', () => {
    for (const odd of [2, 0, true, {}, [], ['lead:2'], { origin: 'lead:2' }, () => 'lead:2', 'lead:', 'lead:abc', 'lead:-1', 'lead:0', 'lead:00', 'lead:01', 'lead:1.5', 'lead:1000', 'lead:2 ', ' lead:2', 'LEAD:2', 'Lead:2', 'lead :2', 'lead:2\n', 'lead:2\u202E', 'lead:٢', 'user:2', 'lead', 'lead:1:2', 'xlead:1', 'lead:1x']) {
      expect(leadStep(odd)).toBeUndefined();
    }
  });
});

describe('the words', () => {
  it('say what the badge says and what the task does meanwhile', () => {
    expect(LEAD_ORIGIN_LABEL).toBe('Split off by the team lead');
    expect(LEAD_ORIGIN_WAITS).toBe('It waits in the backlog until you move it on; the lead cannot start it by itself.');
    expect(leadStepText(2)).toBe('Step 2 from a task you made');
  });
});
