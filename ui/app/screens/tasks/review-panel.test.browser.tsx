import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { SpecReviewPayload } from '../../api/extTypes.ts';
import type { Evidence, PendingRequest, TaskDetail } from '../../api/types.ts';
import { expiredLicense } from '../../test/fixtures.ts';
import { byLabel, byText, cleanup, mount, type, waitFor } from '../../test/render.tsx';
import { verdictGate } from './merge-gate.tsx';
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

  describe("the change's size beside the decision (UI review P1-1)", () => {
    const diffEv = (id: number, ins: number, del: number): Evidence => ({ id, taskId: 't1', attempt: 1, kind: 'diff', passed: null, summary: 'diff', data: { Files: 1, Insertions: ins, Deletions: del }, created: id });
    const review = (evidence: unknown[]): PendingRequest[] => [
      { id: 'req-42', nodeId: 'approve', step: 3, spec: { kind: 'review', title: 'Review and merge: T', payload: { files: ['calc.go'], branch: 'drydock/t1', base: 'main', evidence } } },
    ];

    it('reads as words with spaces, and keeps 8 px between every part, the separator included', () => {
      const d = detail({ evidence: [diffEv(5, 4, 1)], pending: review([diffEv(5, 4, 1)]) });
      const r = mount(<ReviewPanel detail={d} onChanged={() => undefined} />, svc());
      const stat = r.$('[data-testid="review-diffstat"]')!;
      expect(stat.textContent).toBe('1 file changed · +4 \u22121');
      const parts = Array.from(stat.children).map((c) => c.getBoundingClientRect());
      expect(parts.length).toBe(4);
      for (let i = 0; i + 1 < parts.length; i++) {
        const gap = parts[i + 1]!.left - parts[i]!.right;
        // the density rule; the same gap on both sides of the dot
        expect(gap >= 7.5 && gap <= 8.5).toBe(true);
      }
      expect(r.$('[data-testid="review-diff-newer"]')).toBeNull();
    });

    it('gives the counts of the newest patch, says when it is newer than the request, and offers no second patch viewer', () => {
      const d = detail({ evidence: [diffEv(5, 4, 1), diffEv(9, 7, 3)], pending: review([diffEv(5, 4, 1)]) });
      const r = mount(<ReviewPanel detail={d} onChanged={() => undefined} />, svc());
      expect(r.$('[data-testid="review-diffstat"]')!.textContent).toBe('1 file changed · +7 \u22123');
      const note = r.$('[data-testid="review-diff-newer"]')!;
      expect(note.getAttribute('role')).toBe('status');
      expect(note.textContent).toContain('A newer patch was recorded after this request');
      // the patch itself is the Diff section's: the card points there
      expect(buttons(r.container)).toContain('See the patch');
      expect(buttons(r.container)).not.toContain('Show the raw patch');
    });

    it('with no diff recorded, counts the files and does not point at a patch', () => {
      const r = mount(<ReviewPanel detail={detail()} onChanged={() => undefined} />, svc());
      expect(r.$('[data-testid="review-diffstat"]')!.textContent).toBe('1 file changed');
      expect(buttons(r.container)).not.toContain('See the patch');
    });
  });

  // audit 2026-10-16, H8: the review card lists file names and a branch an agent chose
  it('writes out characters that are not drawn in the branch, the file names and the title', () => {
    const d = detail({
      pending: [
        { id: 'req-43', nodeId: 'approve', step: 3, spec: { kind: 'review', title: 'Review and merge: T\u202E', payload: { files: ['src/app\u202Egpj.exe', 'ok.go'], branch: 'drydock/t\u200B1', base: 'ma\u2066in' } } },
      ],
    });
    const r = mount(<ReviewPanel detail={d} onChanged={() => undefined} />, svc());
    const card = r.$('[data-testid="pending-request"]')!;
    expect(card.textContent).toContain('src/app\\u{202e}gpj.exe');
    expect(card.textContent).toContain('drydock/t\\u{200b}1');
    expect(card.textContent).toContain('ma\\u{2066}in');
    expect(card.querySelector('h3')!.textContent).toBe('Review and merge: T\\u{202e}');
    expect(card.textContent).not.toMatch(/[\u202E\u200B\u2066]/);
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

// audit 2026-10-16, M28: a blocking gate none of whose checks could judge asks a person instead of passing
describe('The no-verdict question (gate "verdict")', () => {
  const TITLE = 'No check gave a result for the checks of this task: tests skipped: no test command is set; lint error: eslint not found. Fix what stopped them and check again, go on without a result this time, or stop.';
  const payload = {
    gate: 'verdict',
    node: 'checks',
    what: 'the checks of this task',
    diff: 'abc123',
    checks: [
      { name: 'tests', status: 'skipped', reason: 'no test command is set' },
      { name: 'lint', status: 'error', reason: 'eslint not found' },
    ],
    notes: ['agent:a2 asked to go on without a result; only a person can decide that, so the checks were run again'],
  };
  // no default for the payload: an omitted one is a question that has none
  const question = (p: unknown, kind = 'question') =>
    detail({ pending: [{ id: 'v-1', nodeId: 'checks', step: 4, spec: { kind, title: TITLE, payload: p } }] });
  const open = (client: Parameters<typeof svc>[0] = {}, opts: Parameters<typeof svc>[1] = {}, d = question(payload)) => mount(<ReviewPanel detail={d} onChanged={() => undefined} />, svc(client, opts));
  const names = (root: ParentNode) => Array.from(root.querySelectorAll('button')).map((b) => (b.getAttribute('aria-label') ?? b.textContent ?? '').trim());
  const pend = (spec: Partial<PendingRequest['spec']>): PendingRequest => ({ id: 'x', nodeId: 'n', step: 1, spec: { kind: 'question', title: 't', ...spec } });

  it('the guard returns the payload only for a question of the gate verdict, with every field checked', () => {
    expect(verdictGate(pend({ payload }))).toEqual(payload);
    // another gate, another kind, no payload or one of another shape: not this question
    expect(verdictGate(pend({ payload: { ...payload, gate: 'ci' } }))).toBeUndefined();
    expect(verdictGate(pend({ payload: { ...payload, gate: 'merge' } }))).toBeUndefined();
    expect(verdictGate(pend({ kind: 'approval', payload }))).toBeUndefined();
    expect(verdictGate(pend({ kind: 'review', payload }))).toBeUndefined();
    for (const bad of [undefined, null, 'verdict', 7, ['verdict'], {}, { gate: 'Verdict' }, { gate: 'verdict ' }]) expect(verdictGate(pend({ payload: bad }))).toBeUndefined();
    // checks and notes that are missing or of another type are empty lists; fields of another type are empty text
    expect(verdictGate(pend({ payload: { gate: 'verdict' } }))).toEqual({ gate: 'verdict', node: '', what: '', diff: '', checks: [], notes: [] });
    expect(verdictGate(pend({ payload: { gate: 'verdict', node: 3, what: {}, diff: null, checks: 'tests', notes: { 0: 'x' } } }))).toEqual({ gate: 'verdict', node: '', what: '', diff: '', checks: [], notes: [] });
    expect(verdictGate(pend({ payload: { gate: 'verdict', checks: null, notes: null } }))!.checks).toEqual([]);
    // a check without a name is dropped; the rest of a check is read as far as it is text
    const messy = verdictGate(
      pend({
        payload: {
          gate: 'verdict',
          checks: [null, 5, 'tests', [], {}, { name: '' }, { name: '   ' }, { name: 7 }, { name: 'kept', status: 'weird', reason: 5 }, { name: 'ok', status: 'skipped', reason: 'why' }],
          notes: ['one', 2, null, 'two'],
        },
      }),
    )!;
    expect(messy.checks).toEqual([
      { name: 'kept', status: 'unknown', reason: '' },
      { name: 'ok', status: 'skipped', reason: 'why' },
    ]);
    expect(messy.notes).toEqual(['one', 'two']);
  });

  it('shows the title, what was not checked line by line, the notes, and the three answers with a sentence each', () => {
    const r = open();
    const card = r.$('[data-testid="pending-request"]')!;
    expect(card.getAttribute('data-kind')).toBe('question');
    expect(card.querySelector('h3')!.textContent).toBe(TITLE);
    const gate = r.$('[data-testid="verdict-gate"]')!;
    expect(gate.textContent).toContain('What was not checked:');
    const list = r.$('[data-testid="verdict-gate-checks"]')!;
    expect(Array.from(list.querySelectorAll('li')).map((li) => li.textContent)).toEqual(['tests was skipped: no test command is set', 'lint could not run: eslint not found']);
    expect(list.getAttribute('aria-labelledby')).toBe(gate.querySelector('.what')!.id);
    expect(r.$('[data-testid="verdict-gate-notes"]')!.textContent).toContain('only a person can decide that, so the checks were run again');
    expect(names(r.container)).toEqual(['Check again', 'Go on without a result this time', 'Stop']);
    // each answer is described by its sentence
    const described = (name: string) => document.getElementById(byText(r.container, 'button', name).getAttribute('aria-describedby')!)!.textContent;
    expect(described('Check again')).toBe('Use this after you installed the tool or fixed the setting.');
    expect(described('Go on without a result this time')).toBe('The work moves on as if it had been checked, for this exact content only. This is recorded.');
    expect(described('Stop')).toBe('Ends the run.');
    expect(() => byLabel(r.container, 'Your answer')).toThrow(); // not the free-text question
  });

  it('writes out characters that are not drawn in a check\'s name and reason, and shows an empty reason without a colon', () => {
    const r = open(
      {},
      {},
      question({ ...payload, checks: [{ name: 'te\u202Ests', status: 'error', reason: 'ran \u200Bnothing' }, { name: 'lint', status: 'skipped', reason: '' }, { name: 'e2e', status: 'odd', reason: 'x' }], notes: [] }),
    );
    expect(Array.from(r.$$('[data-testid="verdict-gate-checks"] li')).map((li) => li.textContent)).toEqual(['te\\u{202e}sts could not run: ran \\u{200b}nothing', 'lint was skipped.', 'e2e gave no result: x']);
    expect(r.container.textContent).not.toMatch(/[\u202E\u200B]/);
    expect(r.$('[data-testid="verdict-gate-notes"]')).toBeNull();
  });

  it('a payload without checks says nothing was there to check and still offers the answers', () => {
    const r = open({}, {}, question({ gate: 'verdict', checks: 'none', notes: 7 }));
    expect(r.$('[data-testid="verdict-gate-checks"]')!.textContent).toBe('Nothing was there to check.');
    expect(r.$('[data-testid="verdict-gate-checks"]')!.tagName).toBe('P');
    expect(names(r.container)).toEqual(['Check again', 'Go on without a result this time', 'Stop']);
  });

  it('Check again answers the request with decision answered, once, with one click', async () => {
    const runRespond = recorder(() => undefined);
    const r = open({ runRespond });
    const again = byText<HTMLButtonElement>(r.container, 'button', 'Check again');
    await r.act(() => {
      again.click();
      again.click();
    });
    await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'v-1', 'answered', {}]]), r);
    expect(r.$('[role="status"]')!.textContent).toContain('Answer sent');
  });

  it('Go on without a result asks first, names what happens, and only the second click sends the waiver, once', async () => {
    const runRespond = recorder(() => undefined);
    const r = open({ runRespond });
    await r.act(() => byText(r.container, 'button', 'Go on without a result this time').click());
    expect(runRespond.calls).toEqual([]);
    const ask = r.$('[data-testid="verdict-confirm"]')!;
    expect(ask.getAttribute('data-asking')).toBe('waive');
    expect(ask.getAttribute('aria-label')).toBe('Go on without a result for the checks of this task? It applies to this exact content only, and it is recorded with your name.');
    expect(ask.textContent).toContain('recorded with your name');
    expect(names(r.container)).toEqual(['Yes, go on without a result', 'Go back']);
    // the first button of the question has focus, and Go back leaves everything as it was, with focus where it was
    await waitFor(() => expect(document.activeElement).toBe(byText(r.container, 'button', 'Go back')), r);
    await r.act(() => byText(r.container, 'button', 'Go back').click());
    await waitFor(() => expect(names(r.container)).toEqual(['Check again', 'Go on without a result this time', 'Stop']), r);
    await waitFor(() => expect(document.activeElement).toBe(byText(r.container, 'button', 'Go on without a result this time')), r);
    expect(runRespond.calls).toEqual([]);
    // Yes: approved with the waiver, once
    await r.act(() => byText(r.container, 'button', 'Go on without a result this time').click());
    await r.act(() => {
      const yes = byText(r.container, 'button', 'Yes, go on without a result');
      yes.click();
      yes.click();
    });
    await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'v-1', 'approved', { waive_verdict: true }]]), r);
  });

  it('Stop asks first too, and only the second click rejects the request, once', async () => {
    const runRespond = recorder(() => undefined);
    const r = open({ runRespond });
    await r.act(() => byText(r.container, 'button', 'Stop').click());
    expect(runRespond.calls).toEqual([]);
    expect(r.$('[data-testid="verdict-confirm"]')!.getAttribute('data-asking')).toBe('stop');
    expect(r.$('[data-testid="verdict-confirm"]')!.getAttribute('aria-label')).toBe('Stop the run? It ends here.');
    expect(names(r.container)).toEqual(['Yes, stop the run', 'Go back']);
    await r.act(() => byText(r.container, 'button', 'Go back').click());
    await waitFor(() => expect(document.activeElement).toBe(byText(r.container, 'button', 'Stop')), r);
    expect(runRespond.calls).toEqual([]);
    await r.act(() => byText(r.container, 'button', 'Stop').click());
    await r.act(() => {
      const yes = byText(r.container, 'button', 'Yes, stop the run');
      yes.click();
      yes.click();
    });
    await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'v-1', 'rejected', undefined]]), r);
  });

  it('an error from the core is shown, and the question stays open and usable: nothing is lost, and Yes can be pressed again', async () => {
    let fail = true;
    const calls: unknown[][] = [];
    const runRespond = async (...a: unknown[]) => {
      calls.push(a);
      if (fail) throw new Error('request is no longer pending');
    };
    const r = open({ runRespond: runRespond as never });
    await r.act(() => byText(r.container, 'button', 'Go on without a result this time').click());
    await r.act(() => byText(r.container, 'button', 'Yes, go on without a result').click());
    await waitFor(() => expect(r.$('[role="alert"]')!.textContent).toContain('request is no longer pending'), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Yes, go on without a result').disabled).toBe(false);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Go back').disabled).toBe(false);
    fail = false;
    await r.act(() => byText(r.container, 'button', 'Yes, go on without a result').click());
    await waitFor(() => expect(calls).toHaveLength(2), r);
    expect(calls[1]).toEqual(['run-7', 'v-1', 'approved', { waive_verdict: true }]);
    // the same for Check again
    cleanup();
    const again = open({ runRespond: (async () => { throw new Error('refused'); }) as never });
    await again.act(() => byText(again.container, 'button', 'Check again').click());
    await waitFor(() => expect(again.$('[role="alert"]')!.textContent).toContain('refused'), again);
    expect(byText<HTMLButtonElement>(again.container, 'button', 'Check again').disabled).toBe(false);
  });

  it('read-only allows stopping, not checking again or going on without a result', async () => {
    const runRespond = recorder(() => undefined);
    const r = open({ runRespond }, { license: expiredLicense });
    await waitFor(() => expect(r.$('[data-testid="readonly-note"]')).toBeTruthy(), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Check again').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Go on without a result this time').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Stop').disabled).toBe(false);
    await r.act(() => byText(r.container, 'button', 'Stop').click());
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Yes, stop the run').disabled).toBe(false);
    await r.act(() => byText(r.container, 'button', 'Yes, stop the run').click());
    await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'v-1', 'rejected', undefined]]), r);
  });

  it('other gates and plain questions do not get this panel', () => {
    const ci = open({}, {}, question({ gate: 'ci', pr: 7, head: 'abc', reason: 'none' }));
    expect(ci.$('[data-testid="verdict-gate"]')).toBeNull();
    expect(ci.$('[data-testid="ci-gate"]')).toBeTruthy();
    cleanup();
    const plain = open({}, {}, question(undefined));
    expect(plain.$('[data-testid="verdict-gate"]')).toBeNull();
    expect(byLabel(plain.container, 'Your answer')).toBeTruthy();
    cleanup();
    const approval = open({}, {}, question(payload, 'approval'));
    expect(approval.$('[data-testid="verdict-gate"]')).toBeNull();
    expect(names(approval.container)).toEqual(['Approve', 'Reject']);
  });
});

describe('Permission request', () => {
  const permission = (payload: Record<string, unknown>) =>
    detail({
      run: { id: 'run-7', workflow: 'iterate-until-verified', status: 'RUNNING', topology: 'x', created: 0, updated: 0, version: 1 },
      pending: [{ id: 'apr_1', nodeId: '', step: 0, spec: { kind: 'approval', title: 'Permission request: Bash: npm install left-pad', payload } }],
    });
  const bash = {
    gate: 'permission_prompt',
    tool: 'Bash',
    input: 'npm install left-pad',
    kind: 'shell_command',
    reason: 'A Claude worker asks to use Bash, which its rules do not allow.',
    agentId: 'agt_1',
    expiresAt: Date.UTC(2026, 9, 11, 12, 30),
  };

  it('shows the tool and its exact input, and answers the request id with Allow once or Refuse', async () => {
    for (const [label, decision] of [
      ['Allow once', 'approved'],
      ['Refuse', 'rejected'],
    ] as const) {
      const runRespond = recorder(() => undefined);
      const r = mount(<ReviewPanel detail={permission(bash)} onChanged={() => undefined} />, svc({ runRespond }));
      const panel = r.$('[data-testid="permission-prompt"]')!;
      expect(panel.getAttribute('data-tool')).toBe('Bash');
      expect(r.$('[data-testid="permission-prompt-input"]')!.textContent).toBe('npm install left-pad');
      expect(r.$('[aria-label="Bash input"]')).toBeTruthy();
      expect(panel.textContent).toContain('shell_command');
      expect(r.$('[data-testid="permission-prompt-reason"]')!.textContent).toContain('its rules do not allow');
      expect(panel.textContent).toContain('Refused if unanswered by');
      expect(buttons(r.container)).toEqual(['Allow once', 'Refuse']);
      expect(runRespond.calls).toEqual([]); // nothing is allowed by showing it
      await r.act(() => byText(r.container, 'button', label).click());
      await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'apr_1', decision, undefined]]), r);
      cleanup();
    }
  });

  it('shows a JSON input indented and as text', () => {
    const r = mount(
      <ReviewPanel detail={permission({ ...bash, tool: 'WebFetch', input: '{"prompt":"<b>x</b>","url":"https://example.com"}', kind: 'agent_tool' })} onChanged={() => undefined} />,
      svc(),
    );
    const pre = r.$('[data-testid="permission-prompt-input"]')!;
    expect(pre.textContent).toBe('{\n  "prompt": "<b>x</b>",\n  "url": "https://example.com"\n}');
    expect(pre.querySelector('b')).toBe(null);
    cleanup();
  });

  // audit 2026-10-16, H8: the destination was sorted behind the body and cut off with it
  it('shows where a call goes before its body, says when part of the input is cut, and writes out characters that are not drawn', () => {
    const write = {
      ...bash,
      tool: 'Write',
      kind: 'fs_outside_worktree',
      targets: ['C:\\Users\\kyle\\.ssh\\authorized_keys'],
      shortened: true,
      input: '{"file_path":"C:\\\\Users\\\\kyle\\\\.ssh\\\\authorized_keys","content":"ssh-ed25519 AAAA...[48213 bytes not shown]...kyle@pc"}',
    };
    const r = mount(<ReviewPanel detail={permission(write)} onChanged={() => undefined} />, svc());
    expect(r.$('[data-testid="permission-prompt-targets"]')!.textContent).toBe('C:\\Users\\kyle\\.ssh\\authorized_keys');
    const note = r.$('[data-testid="permission-prompt-shortened"]')!;
    expect(note.textContent).toContain('Part of this input is not shown');
    expect(note.textContent).toContain('including the part you cannot see');
    const text = r.$('[data-testid="permission-prompt-input"]')!.textContent!;
    expect(text.indexOf('file_path')).toBeLessThan(text.indexOf('content'));
    expect(text).toContain('[48213 bytes not shown]');
    expect(r.$('[data-testid="permission-prompt-hidden"]')).toBeNull();
    expect(buttons(r.container)).toEqual(['Allow once', 'Refuse']); // the choice stays with the person
    cleanup();

    // a right-to-left override and a zero-width space in the path and in the body are written out, and said so
    const tricky = mount(
      <ReviewPanel
        detail={permission({ ...bash, tool: 'Edit', targets: ['src/app\u202Egpj.exe'], input: '{"file_path":"src/app\u202Egpj.exe","new_string":"a\u200Bb"}' })}
        onChanged={() => undefined}
      />,
      svc(),
    );
    expect(tricky.$('[data-testid="permission-prompt-targets"]')!.textContent).toBe('src/app\\u{202e}gpj.exe');
    const shown = tricky.$('[data-testid="permission-prompt-input"]')!.textContent!;
    expect(shown).toContain('src/app\\u{202e}gpj.exe');
    expect(shown).toContain('a\\u{200b}b');
    expect(shown).not.toContain('\u202E');
    expect(tricky.$('[data-testid="permission-prompt-hidden"]')!.textContent).toContain('not drawn');
    expect(tricky.$('[data-testid="permission-prompt-shortened"]')).toBeNull();
  });

  it('offers Edit before allowing only where the core says the call can change, and sends the changed input (P88)', async () => {
    // not editable (an ACP agent, a shortened or redacted input): no edit button
    const plain = mount(<ReviewPanel detail={permission(bash)} onChanged={() => undefined} />, svc());
    expect(buttons(plain.container)).toEqual(['Allow once', 'Refuse']);
    cleanup();

    const runRespond = recorder(() => undefined);
    const r = mount(<ReviewPanel detail={permission({ ...bash, editable: true, shell: true })} onChanged={() => undefined} />, svc({ runRespond }));
    expect(buttons(r.container)).toEqual(['Allow once', 'Edit before allowing', 'Refuse']);
    await r.act(() => byText(r.container, 'button', 'Edit before allowing').click());
    const box = r.$('[data-testid="permission-prompt-edit"]') as HTMLTextAreaElement;
    expect(box.value).toBe('npm install left-pad');
    expect(buttons(r.container)).toEqual(['Allow the changed call once', 'Cancel the change', 'Refuse']);
    // an empty command is not sent
    await r.act(() => type(box, '   '));
    await r.act(() => byText(r.container, 'button', 'Allow the changed call once').click());
    expect(r.$('[data-testid="permission-prompt-edit-error"]')!.textContent).toBe('Write the command to run.');
    expect(runRespond.calls).toEqual([]);
    await r.act(() => type(box, 'npm install left-pad@1.3.0 --ignore-scripts'));
    await r.act(() => byText(r.container, 'button', 'Allow the changed call once').click());
    await waitFor(() => expect(runRespond.calls).toEqual([['run-7', 'apr_1', 'approved', { input: { command: 'npm install left-pad@1.3.0 --ignore-scripts' } }]]), r);
    cleanup();

    // a JSON input is changed as JSON: it must stay an object
    const json = recorder(() => undefined);
    const w = mount(
      <ReviewPanel detail={permission({ ...bash, tool: 'Write', shell: false, editable: true, input: '{"file_path":"a.txt","content":"x"}' })} onChanged={() => undefined} />,
      svc({ runRespond: json }),
    );
    await w.act(() => byText(w.container, 'button', 'Edit before allowing').click());
    const area = w.$('[data-testid="permission-prompt-edit"]') as HTMLTextAreaElement;
    expect(JSON.parse(area.value)).toEqual({ file_path: 'a.txt', content: 'x' });
    for (const [bad, said] of [
      ['{"file_path":', 'The input is not valid JSON.'],
      ['["a"]', 'The input must be a JSON object ({ ... }).'],
    ] as const) {
      await w.act(() => type(area, bad));
      await w.act(() => byText(w.container, 'button', 'Allow the changed call once').click());
      expect(w.$('[data-testid="permission-prompt-edit-error"]')!.textContent).toBe(said);
    }
    // Cancel the change goes back to the plain answers
    await w.act(() => byText(w.container, 'button', 'Cancel the change').click());
    expect(w.$('[data-testid="permission-prompt-edit"]')).toBeNull();
    await w.act(() => byText(w.container, 'button', 'Edit before allowing').click());
    await w.act(() => type(w.$('[data-testid="permission-prompt-edit"]') as HTMLTextAreaElement, '{"file_path":"b.txt","content":"y"}'));
    await w.act(() => byText(w.container, 'button', 'Allow the changed call once').click());
    await waitFor(() => expect(json.calls).toEqual([['run-7', 'apr_1', 'approved', { input: { file_path: 'b.txt', content: 'y' } }]]), w);
  });

  it('read-only allows refusing, not allowing', async () => {
    const r = mount(<ReviewPanel detail={permission(bash)} onChanged={() => undefined} />, svc({}, { license: expiredLicense }));
    await waitFor(() => expect(r.$('[data-testid="readonly-note"]')).toBeTruthy(), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Allow once').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Refuse').disabled).toBe(false);
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
