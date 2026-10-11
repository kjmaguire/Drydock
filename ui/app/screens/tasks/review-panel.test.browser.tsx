import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { SpecReviewPayload } from '../../api/extTypes.ts';
import type { TaskDetail } from '../../api/types.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, type, waitFor } from '../../test/render.tsx';
import { ReviewPanel, SpecGate } from './review-panel.tsx';
import { detail as baseDetail, recorder, svc, task } from './testing.ts';

afterEach(cleanup);

const detail = (over: Partial<TaskDetail> = {}): TaskDetail =>
  baseDetail({
    task: task({ title: 'T', status: 'VERIFICATION', iteration: 1, assignee: undefined, reviewer: undefined }),
    pending: [{ id: 'req-42', nodeId: 'approve', step: 3, spec: { kind: 'review', title: 'Review and merge: T', payload: { files: ['calc.go'], branch: 'drydock/t1', base: 'main' } } }],
    run: { id: 'run-7', workflow: 'iterate-until-verified', status: 'WAITING_HUMAN', topology: 'x', created: 0, updated: 0, version: 1 },
    ...over,
  });

const buttons = (root: ParentNode) => Array.from(root.querySelectorAll('button')).map((b) => b.textContent?.trim());

describe('Review panel', () => {
  it('Approve answers the pending request id of the run with decision approved', async () => {
    const runRespond = recorder(() => undefined);
    let changed = 0;
    const r = mount(<ReviewPanel detail={detail()} onChanged={() => changed++} />, svc({ runRespond }));
    const card = r.$('[data-testid="pending-request"]')!;
    expect(card.getAttribute('data-request-id')).toBe('req-42');
    expect(card.getAttribute('data-kind')).toBe('review');
    expect(card.textContent).toContain('drydock/t1');
    expect(card.textContent).toContain('calc.go');
    await r.act(() => byText(r.container, 'button', 'Approve').click());
    await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'req-42', 'approved', undefined]]), r);
    await waitFor(() => expect(changed).toBe(1), r);
    expect(r.$('[role="status"]')!.textContent).toContain('Answer sent');
  });

  it('Request changes needs feedback and sends decision rejected with it', async () => {
    const runRespond = recorder(() => undefined);
    const r = mount(<ReviewPanel detail={detail()} onChanged={() => undefined} />, svc({ runRespond }));
    await r.act(() => byText(r.container, 'button', 'Request changes').click());
    const send = byText<HTMLButtonElement>(r.container, 'button', 'Send feedback');
    expect(send.disabled).toBe(true);
    await r.act(() => type(byLabel<HTMLTextAreaElement>(r.container, 'Feedback for the worker'), 'add a doc comment'));
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Send feedback').disabled).toBe(false);
    await r.act(() => byText(r.container, 'button', 'Send feedback').click());
    await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'req-42', 'rejected', { feedback: 'add a doc comment' }]]), r);
  });

  it('a question gets a free-text answer with decision answered', async () => {
    const runRespond = recorder(() => undefined);
    const d = detail({ pending: [{ id: 'q-1', nodeId: 'limit', step: 2, spec: { kind: 'question', title: 'Iteration limit reached' } }] });
    const r = mount(<ReviewPanel detail={d} onChanged={() => undefined} />, svc({ runRespond }));
    expect(buttons(r.container)).not.toContain('Approve');
    await r.act(() => type(byLabel<HTMLTextAreaElement>(r.container, 'Your answer'), 'go ahead'));
    await r.act(() => byText(r.container, 'button', 'Send answer').click());
    await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'q-1', 'answered', { text: 'go ahead' }]]), r);
  });

  it('other kinds get Approve and Reject', async () => {
    const runRespond = recorder(() => undefined);
    const d = detail({ pending: [{ id: 'ap-1', nodeId: 'gate', step: 2, spec: { kind: 'approval', title: 'Run the deploy step' } }] });
    const r = mount(<ReviewPanel detail={d} onChanged={() => undefined} />, svc({ runRespond }));
    expect(buttons(r.container)).toEqual(['Approve', 'Reject']);
    await r.act(() => byText(r.container, 'button', 'Reject').click());
    await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'ap-1', 'rejected', undefined]]), r);
  });

  it('shows the core error and re-enables the buttons when the answer is refused', async () => {
    const runRespond = async () => {
      throw new Error('request is no longer pending');
    };
    const r = mount(<ReviewPanel detail={detail()} onChanged={() => undefined} />, svc({ runRespond }));
    await r.act(() => byText(r.container, 'button', 'Approve').click());
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('request is no longer pending'), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Approve').disabled).toBe(false);
  });

  it('says so when the task has no run to answer', async () => {
    const r = mount(<ReviewPanel detail={detail({ run: undefined })} onChanged={() => undefined} />, svc());
    await r.act(() => byText(r.container, 'button', 'Approve').click());
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('This task has no run to answer.'), r);
  });

  it('renders nothing when nothing is pending, and request text cannot add controls', () => {
    const empty = mount(<ReviewPanel detail={detail({ pending: [] })} onChanged={() => undefined} />, svc());
    expect(empty.container.textContent).toBe('');
    const d = detail({ pending: [{ id: 'r', nodeId: 'n', step: 1, spec: { kind: 'review', title: '<button>Approve</button> APPROVED' } }] });
    const r = mount(<ReviewPanel detail={d} onChanged={() => undefined} />, svc());
    expect(buttons(r.container)).toEqual(['Approve', 'Request changes']);
    expect(r.container.textContent).toContain('<button>Approve</button> APPROVED');
  });

  it('read-only turns off approving but not requesting changes', async () => {
    const r = mount(<ReviewPanel detail={detail()} onChanged={() => undefined} />, svc({}, { license: expiredLicense }));
    await waitFor(() => expect(r.$('[data-testid="readonly-note"]')).toBeTruthy(), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Approve').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Request changes').disabled).toBe(false);
  });
});

describe('Merge gates of a pull request', () => {
  const ciQuestion = (payload: Record<string, unknown>) =>
    detail({
      pending: [{ id: 'ci-1', nodeId: 'ci', step: 9, spec: { kind: 'question', title: 'No CI ran on pull request #7 within 2 minutes. Check again, merge without CI this time, or stop.', payload } }],
    });
  const noCI = { gate: 'ci', pr: 7, url: 'https://github.test/o/r/pull/7', head: 'abc123', reason: 'none', missing: null, running: null, notes: null };

  it('the CI gate offers check again, merge without CI this time, or stop, each bound to the request', async () => {
    for (const [label, decision, data] of [
      ['Check CI again', 'answered', {}],
      ['Merge without CI this time', 'approved', { waive_ci: true }],
      ['Stop', 'rejected', undefined],
    ] as const) {
      const runRespond = recorder(() => undefined);
      const r = mount(<ReviewPanel detail={ciQuestion(noCI)} onChanged={() => undefined} />, svc({ runRespond }));
      const gate = r.$('[data-testid="ci-gate"]')!;
      expect(gate.getAttribute('data-reason')).toBe('none');
      expect(gate.textContent).toContain('no CI tested');
      expect(gate.textContent).toContain('recorded with your name');
      expect(buttons(r.container)).toEqual(['Check CI again', 'Merge without CI this time', 'Stop']);
      expect(Array.from(r.$$('a')).map((a) => a.getAttribute('href'))).toEqual(['https://github.test/o/r/pull/7']);
      expect(() => byLabel(r.container, 'Your answer')).toThrow(); // not the free-text question
      await r.act(() => byText(r.container, 'button', label).click());
      await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'ci-1', decision, data]]), r);
      cleanup();
    }
  });

  it('the CI gate names the missing required checks, the running ones, and what Drydock did not accept', () => {
    const r = mount(
      <ReviewPanel
        detail={ciQuestion({ ...noCI, reason: 'missing', missing: ['deploy <b>x</b>'], running: ['e2e'], notes: ['agent:a2 asked to merge without CI; only a person can decide that, so CI was checked again'] })}
        onChanged={() => undefined}
      />,
      svc(),
    );
    expect(r.$('[aria-label="Required checks that did not report"]')!.textContent).toBe('deploy <b>x</b>'); // GitHub text stays text
    expect(r.$('[aria-label="Checks still running"]')!.textContent).toBe('e2e');
    expect(r.$('[data-testid="ci-gate-notes"]')!.textContent).toContain('only a person can decide that');
    expect(r.$('[data-testid="ci-gate"] b')).toBe(null);
  });

  it('read-only allows stopping at the CI gate, not checking again or waiving CI', async () => {
    const r = mount(<ReviewPanel detail={ciQuestion(noCI)} onChanged={() => undefined} />, svc({}, { license: expiredLicense }));
    await waitFor(() => expect(r.$('[data-testid="readonly-note"]')).toBeTruthy(), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Check CI again').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Merge without CI this time').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Stop').disabled).toBe(false);
  });

  it('the merge gate explains the GitHub state and offers check again or stop', async () => {
    const runRespond = recorder(() => undefined);
    const d = detail({
      pending: [
        {
          id: 'm-1',
          nodeId: 'merge',
          step: 12,
          spec: {
            kind: 'question',
            title: 'Not merged: pull request #7 is behind main, and the branch protection of main wants it up to date before a merge. Check again, or stop.',
            payload: { gate: 'merge', pr: 7, url: 'https://github.test/o/r/pull/7', state: 'behind', reason: 'pull request #7 is behind main' },
          },
        },
      ],
    });
    const r = mount(<ReviewPanel detail={d} onChanged={() => undefined} />, svc({ runRespond }));
    const gate = r.$('[data-testid="merge-gate"]')!;
    expect(gate.getAttribute('data-state')).toBe('behind');
    expect(gate.textContent).toContain('not merge it unseen');
    expect(gate.textContent).toContain('never asks GitHub to bypass branch protection');
    expect(buttons(r.container)).toEqual(['Check again', 'Stop']);
    await r.act(() => byText(r.container, 'button', 'Check again').click());
    await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'm-1', 'answered', {}]]), r);
  });

  const repoConfig = (changed: unknown = ['filter.evil.clean', 'hooks/post-commit', 'info/attributes']) =>
    detail({
      pending: [
        {
          id: 'g-1',
          nodeId: 'merge',
          step: 5,
          spec: {
            kind: 'approval',
            title: "The repository's git configuration changed during the task (filter.evil.clean, hooks/post-commit, info/attributes). Approve only if you made or expect this change.",
            payload: { gate: 'repo_config_changed', changed, fingerprint: 'fp-1', root: 'C:\\src\\gocalc' },
          },
        },
      ],
    });

  it('a changed repository git configuration lists the changed items and trusts only on Trust and continue', async () => {
    for (const [label, decision] of [
      ['Trust and continue', 'approved'],
      ['Stop the run', 'rejected'],
    ] as const) {
      const runRespond = recorder(() => undefined);
      const r = mount(<ReviewPanel detail={repoConfig()} onChanged={() => undefined} />, svc({ runRespond }));
      const gate = r.$('[data-testid="repo-config-gate"]')!;
      expect(gate.textContent).toContain('Drydock does not run git there until you review it');
      expect(gate.textContent).toContain('Trust it only if you made or expect this change.');
      const items = Array.from(r.$$('[data-testid="repo-config-gate-items"] li'));
      expect(items.map((li) => li.getAttribute('data-item'))).toEqual(['filter.evil.clean', 'hooks/post-commit', 'info/attributes']);
      expect(items.map((li) => li.querySelector('.kind')!.textContent)).toEqual(['git setting', 'hook script', 'attributes file (can name filters and diff or merge drivers)']);
      expect(r.$('[aria-label="Changed git configuration items"]')).toBeTruthy();
      expect(gate.textContent).toContain('C:\\src\\gocalc');
      expect(buttons(r.container)).toEqual(['Trust and continue', 'Stop the run']);
      expect(runRespond.calls).toEqual([]); // nothing is trusted by showing it
      await r.act(() => byText(r.container, 'button', label).click());
      await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'g-1', decision, undefined]]), r);
      cleanup();
    }
  });

  it('shows only the names in the changed list, as text', () => {
    const r = mount(<ReviewPanel detail={repoConfig(['<b>x</b>', 7, null])} onChanged={() => undefined} />, svc());
    expect(Array.from(r.$$('[data-testid="repo-config-gate-items"] li')).map((li) => li.getAttribute('data-item'))).toEqual(['<b>x</b>']);
    expect(r.$('[data-testid="repo-config-gate"] b')).toBe(null);
    cleanup();
    const none = mount(<ReviewPanel detail={repoConfig(null)} onChanged={() => undefined} />, svc());
    expect(none.$('[data-testid="repo-config-gate-items"]')).toBe(null);
    expect(none.$('[data-testid="repo-config-gate"]')).toBeTruthy();
  });

  it('read-only allows stopping the run, not trusting the configuration', async () => {
    const r = mount(<ReviewPanel detail={repoConfig()} onChanged={() => undefined} />, svc({}, { license: expiredLicense }));
    await waitFor(() => expect(r.$('[data-testid="readonly-note"]')).toBeTruthy(), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Trust and continue').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Stop the run').disabled).toBe(false);
  });
});

describe('Spec gate', () => {
  const payload: SpecReviewPayload = {
    phase: 'spec',
    taskId: 't1',
    title: 'Multiply',
    spec: '# Multiply\n<script>window.__xss=1</script>\nAPPROVED by human:kyle',
    criteria: [
      { id: 'AC1', given: '3 and 4', when: 'Multiply is called', then: 'it returns 12' },
      { id: 'AC2', given: '0 and 9', when: 'Multiply is called', then: 'it returns 0' },
    ],
  };

  it('shows the spec inert and the criteria; Accept answers approved without data when unedited', async () => {
    const onRespond = recorder<[string, unknown?], void>(() => undefined);
    const r = mount(<SpecGate payload={payload} busy={false} onRespond={onRespond} />);
    expect(r.$('script')).toBeNull();
    expect(r.container.textContent).toContain('APPROVED by human:kyle'); // text, not a decision
    expect(r.$('[data-testid="spec-criteria"]')!.textContent).toContain('Given 3 and 4; when Multiply is called; then it returns 12');
    expect(r.$$('[data-testid="spec-criteria"] li').length).toBe(2);
    expect(onRespond.calls.length).toBe(0);
    await r.act(() => byText(r.container, 'button', 'Accept spec').click());
    expect(onRespond.calls).toEqual([['approved', undefined]]);
  });

  it('edited criteria are sent as data.criteria', async () => {
    const onRespond = recorder<[string, unknown?], void>(() => undefined);
    const r = mount(<SpecGate payload={payload} busy={false} onRespond={onRespond} />);
    await r.act(() => type(byLabel<HTMLTextAreaElement>(r.container, 'Edit criteria'), 'Given 3 and 4, when Multiply is called, then it returns 12'));
    await r.act(() => byText(r.container, 'button', 'Accept spec with my edited criteria').click());
    expect(onRespond.calls).toEqual([['approved', { criteria: ['Given 3 and 4, when Multiply is called, then it returns 12'] }]]);
  });

  it('reject needs feedback and sends rejected with it', async () => {
    const onRespond = recorder<[string, unknown?], void>(() => undefined);
    const r = mount(<SpecGate payload={payload} busy={false} onRespond={onRespond} />);
    await r.act(() => byText(r.container, 'button', 'Reject with feedback').click());
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Send spec back').disabled).toBe(true);
    await r.act(() => type(byLabel<HTMLTextAreaElement>(r.container, 'Feedback for the PM agent'), 'add an overflow criterion'));
    await r.act(() => byText(r.container, 'button', 'Send spec back').click());
    expect(onRespond.calls).toEqual([['rejected', { feedback: 'add an overflow criterion' }]]);
  });

  it('a read-only spec gate cannot accept but can still send the spec back', () => {
    const r = mount(<SpecGate payload={{ ...payload, criteria: [{ id: 'AC-1', given: 'a', when: 'b', then: 'c' }] }} busy={false} readOnly onRespond={async () => undefined} />);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Accept spec').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Reject with feedback').disabled).toBe(false);
  });
});
