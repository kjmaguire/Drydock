import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { directionText, hookOutcomeText, PROPERTY_FIELDS, sizeText, TASK_STATUSES } from './integrations.ts';

describe('decision P114 helpers', () => {
  it('says what the receiver did with a delivery in words, never its body', () => {
    expect(hookOutcomeText('queued')).toBe('item queued for a read');
    expect(hookOutcomeText('refused_signature')).toBe('refused: wrong signature');
    expect(hookOutcomeText('refused_replay')).toBe('refused: seen before');
    expect(hookOutcomeText('refused_stale')).toBe('refused: too old or not dated');
    expect(hookOutcomeText('something_new')).toBe('something_new');
  });
  it('names the way a status mapping is used', () => {
    expect(directionText('to_vendor')).toBe('Drydock to the service');
    expect(directionText('from_vendor')).toBe('the service to Drydock (you apply it)');
    expect(directionText('both')).toBe('both ways');
    expect(directionText(undefined)).toBe('both ways');
  });
  it('the statuses and fields match the core (internal/integrations TaskStatuses, propertyFields)', () => {
    expect([...TASK_STATUSES]).toEqual(['READY', 'CLAIMED', 'IN_PROGRESS', 'BLOCKED', 'WAITING_HUMAN', 'REVIEW', 'VERIFICATION', 'COMPLETED', 'FAILED', 'CANCELLED']);
    expect([...PROPERTY_FIELDS]).toEqual(['state', 'priority', 'assignee', 'labels']);
  });
  it('sizes in words', () => {
    expect(sizeText(0)).toBe('size unknown');
    expect(sizeText(512)).toBe('512 B');
    expect(sizeText(2048)).toBe('2 KB');
    expect(sizeText(5 * 1024 * 1024)).toBe('5 MB');
  });
});
