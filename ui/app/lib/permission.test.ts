import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { confirmationFor, isNeedsConfirmation, needsConfirmation, SKIP_CONFIRM_LABEL, SKIP_MODES, skipsApprovals, splitConfirmation } from './permission.ts';

describe('permission modes that skip approvals (M1)', () => {
  it('are bypassPermissions, auto and dontAsk, as the core counts them, and no other mode', () => {
    expect([...SKIP_MODES].sort()).toEqual(['auto', 'bypassPermissions', 'dontAsk']);
    for (const m of ['bypassPermissions', 'auto', 'dontAsk']) expect(skipsApprovals(m)).toBe(true);
    for (const m of ['', 'default', 'acceptEdits', 'plan', 'Auto', 'bypass', undefined]) expect(skipsApprovals(m)).toBe(false);
  });

  it('need the person\'s confirmation until the box is ticked for exactly that mode', () => {
    expect(needsConfirmation('bypassPermissions', '')).toBe(true);
    expect(needsConfirmation('bypassPermissions', undefined)).toBe(true);
    expect(needsConfirmation('bypassPermissions', 'bypassPermissions')).toBe(false);
    // ticked for another mode that skips approvals: choosing this one asks again
    expect(needsConfirmation('auto', 'bypassPermissions')).toBe(true);
    // a mode that keeps approvals never asks, whatever the box says
    expect(needsConfirmation('acceptEdits', '')).toBe(false);
    expect(needsConfirmation('', '')).toBe(false);
    expect(needsConfirmation(undefined, undefined)).toBe(false);
  });

  it('add confirmSkipsApprovals: true to a request only for a mode that skips approvals and was ticked for', () => {
    expect(confirmationFor('bypassPermissions', 'bypassPermissions')).toEqual({ confirmSkipsApprovals: true });
    expect(confirmationFor('dontAsk', 'dontAsk')).toEqual({ confirmSkipsApprovals: true });
    expect(confirmationFor('bypassPermissions', '')).toEqual({});
    expect(confirmationFor('auto', 'bypassPermissions')).toEqual({});
    // a box that was ticked and then the mode changed to one that keeps approvals: nothing is sent
    expect(confirmationFor('plan', 'bypassPermissions')).toEqual({});
    expect(confirmationFor('plan', 'plan')).toEqual({});
    expect(confirmationFor(undefined, undefined)).toEqual({});
  });

  it('read the core\'s refusal by its words and keep it apart from other failures', () => {
    const refusal = new Error('needs your confirmation: the permission mode "bypassPermissions" lets this agent act without asking you first. Confirm it (confirmSkipsApprovals) to use it');
    const other = new Error('unknown adapter "x"');
    expect(isNeedsConfirmation(refusal)).toBe(true);
    expect(isNeedsConfirmation(new Error('  Needs your confirmation: x'))).toBe(true);
    expect(isNeedsConfirmation(other)).toBe(false);
    expect(isNeedsConfirmation('needs your confirmation')).toBe(false);
    expect(isNeedsConfirmation(undefined)).toBe(false);
    expect(splitConfirmation(refusal)).toEqual({ confirm: refusal, other: undefined });
    expect(splitConfirmation(other)).toEqual({ confirm: undefined, other });
    expect(splitConfirmation(undefined)).toEqual({ confirm: undefined, other: undefined });
  });

  it('has the box labelled as the finding words it', () => {
    expect(SKIP_CONFIRM_LABEL).toBe('I understand this agent will act without asking me first');
  });
});
