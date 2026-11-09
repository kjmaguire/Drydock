import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Evidence, ReviewRecord } from '../../api/types.ts';
import { cleanup, mount } from '../../test/render.tsx';
import { EvidenceList, ReviewList } from './evidence.tsx';
import { svc } from './testing.ts';

afterEach(cleanup);

const ev = (id: number, kind: string, attempt: number, passed: boolean | null = true): Evidence => ({ id, taskId: 't1', attempt, kind, passed, summary: 'what it found', created: id }) as Evidence;
const review = (id: number, verdict: string, attempt: number): ReviewRecord => ({ id, taskId: 't1', attempt, reviewer: 'a-rev', verdict, feedback: '', created: id });

describe('the Evidence and Reviews lists in words (critique 2026-11-08)', () => {
  it('labels every kind the core records, and says which run only once there is more than one', () => {
    const one = mount(<EvidenceList evidence={[ev(1, 'build', 0), ev(2, 'review_requested', 0, null), ev(3, 'brand_new_kind', 0, null)]} />, svc());
    const rows = Array.from(one.$$('[data-testid="evidence"]'));
    expect(rows.map((li) => li.querySelector('strong')!.textContent)).toEqual(['Build', 'Sent to you for review', 'Brand new kind']);
    // the core's kind names and its 0-based attempt never show
    expect(one.container.textContent).not.toContain('attempt');
    expect(one.container.textContent).not.toContain('review_requested');
    expect(one.container.textContent).not.toContain('run 1');
    cleanup();
    const two = mount(<EvidenceList evidence={[ev(1, 'test', 0, false), ev(2, 'test', 1)]} />, svc());
    expect(Array.from(two.$$('[data-testid="evidence"]'), (li) => li.querySelector('.head span:last-child')!.textContent)).toEqual(['run 1', 'run 2']);
  });

  it("says the reviewer's verdict in words, never SHIP or REVISE", () => {
    const r = mount(<ReviewList reviews={[review(1, 'REVISE', 0), review(2, 'SHIP', 1)]} agents={[]} />, svc());
    const heads = Array.from(r.$$('[data-testid="review"] .head'), (h) => h.textContent);
    expect(heads).toEqual(['Asked for changesby a-revrun 1', 'Approvedby a-revrun 2']);
    expect(r.container.textContent).not.toContain('SHIP');
    expect(r.container.textContent).not.toContain('REVISE');
  });
});
