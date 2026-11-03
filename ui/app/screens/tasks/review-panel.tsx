// The human decision panel: every pending request of the task's run, each answered by buttons bound to the request id (never
// to message text). Spec reviews get the spec gate, budget pauses the resume form, questions a free-text answer, reviews
// Approve / Request changes, the limit question and watchdog pauses Continue (with guidance) / Stop, the pull request's CI
// gate Check CI again / Merge without CI this time / Stop and its merge gate Check again / Stop, a blocking gate none of whose
// checks could judge (gate verdict) the checks that did not run with Check again / Go on without a result this time / Stop (the last
// two asked twice), a changed repository git
// configuration (gate repo_config_changed) its changed items with Trust and continue / Stop the run, a turn cut off by Drydock
// stopping (gate interrupted) what was cut off and the crash report of that moment with Run the step again / Stop the run, a worker's permission
// request (gate permission_prompt) the tool and its exact input with Allow for the rest of this task (where the core allows it) / Allow once / Refuse, a GitHub failure of a pull-request step (gate github) Try again / Stop with its fix, anything else Approve / Reject. Read-only (license) turns off approving and answering; refusing
// work (reject, request changes, send the spec back) stays available.

import { css, on, type Handle } from 'remix/component';
import { FileDiff, GitBranch, Hand } from 'lucide';
import type { SpecReviewPayload } from '../../api/extTypes.ts';
import type { PendingRequest, PRReviewNote, TaskAllowRule, TaskDetail } from '../../api/types.ts';
import { visible } from '../../lib/visible.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { BudgetPausePanel } from '../shared/budget-pause.tsx';
import { LimitPanel, limitPayload, WatchdogPanel, watchdogPayload } from '../shared/escalation.tsx';
import { InterruptedPanel, interruptedPayload } from '../shared/interrupted.tsx';
import { PermissionPromptPanel, permissionPayload } from '../shared/permission-prompt.tsx';
import { RepoConfigGatePanel, repoConfigPayload } from '../shared/repo-config.tsx';
import { CIGatePanel, ciGatePayload, GitHubTroublePanel, githubTroublePayload, MergeGatePanel, mergeGatePayload, VerdictGatePanel, verdictGate } from './merge-gate.tsx';
import { SandboxGatePanel, sandboxGatePayload } from './sandbox-gate.tsx';
import { budgetPayload, criterionLine, editedCriteria, newestDiffStat, payloadOf, type PayloadDiff, specPayload } from './tasks.ts';
import { changeLine, changesDraft, type CriterionRow } from './task-page.ts';
import { plural } from '../../lib/format.ts';

// The decide card (graphite, decision P97): on the task page it sits above the Activity while a request waits, a card in the
// "waiting on you" tone with the requests inside; the primary answer is the accent button.
const panelCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  borderRadius: t.rLg,
  border: `1px solid ${t.mentionBorder}`,
  background: t.mentionBg,
  minWidth: '0',
  '& > h2': { display: 'flex', alignItems: 'center', gap: t.s2, margin: '0', fontSize: t.textMd, fontWeight: '600' },
  '& > h2 svg': { color: t.warning },
});

const cardCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  borderRadius: t.rMd,
  border: `1px solid ${t.border}`,
  background: t.bgSurface,
  minWidth: '0',
  '& > h3': { fontSize: t.textMd, fontWeight: '600', overflowWrap: 'anywhere' },
  '& .meta': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, color: t.textMuted, fontSize: t.textSm },
  '& .meta svg': { color: t.textFaint },
  '& details ul': { margin: `${t.s2} 0 0`, paddingLeft: '20px', fontSize: t.textSm },
  '& summary': { cursor: 'pointer', fontSize: t.textSm, color: t.textMuted },
});

/**
 * The pending requests of the task's run. Renders nothing when nothing is pending. `criteria` are the task page's criteria rows:
 * Request changes starts from the ones not met or still open.
 */
export function ReviewPanel(handle: Handle<{ detail: TaskDetail; criteria?: readonly CriterionRow[]; onChanged: () => void }>) {
  return () => {
    const { detail, criteria = [], onChanged } = handle.props;
    if (detail.pending.length === 0) return null;
    // the task's newest patch: what the Diff section shows and what Approve merges (L36), not the one the request was raised on
    const diff = newestDiffStat(detail.evidence);
    return (
      <section mix={panelCss} aria-label="Needs your decision" data-testid="review-panel">
        <h2>
          <Icon icon={Hand} size={16} />
          Needs your decision
        </h2>
        {detail.pending.map((p) => (
          <PendingCard key={p.id} p={p} runId={detail.run?.id} diff={diff} criteria={criteria} prReview={p.spec.kind === 'review' ? detail.prReview : undefined} onChanged={onChanged} />
        ))}
      </section>
    );
  };
}

/**
 * Whether a request is one of the gates a panel of its own answers (a budget pause, the limit question, a watchdog pause, the CI,
 * merge or verdict gate, a cut-off turn, a changed repository configuration). A worker's permission request is not: it has its
 * own panel with "for the rest of the task".
 */
export function hasGatePanel(p: PendingRequest): boolean {
  if (permissionPayload(p)) return false;
  return !!(budgetPayload(p) || limitPayload(p) || watchdogPayload(p) || ciGatePayload(p) || mergeGatePayload(p) || verdictGate(p) || interruptedPayload(p) || repoConfigPayload(p) || githubTroublePayload(p) || sandboxGatePayload(p));
}

/**
 * The panel that answers a gate (hasGatePanel), bound to the request: the task page's decide card and the Inbox's detail pane
 * (C3) show the same one, so answering in either is the same answer. The waivers keep their second click inside the panels.
 */
export function GateAnswer(
  handle: Handle<{ p: PendingRequest; runId: string | undefined; busy: boolean; readOnly: boolean; onRespond: (decision: string, data?: unknown) => Promise<void>; onDone: () => void }>,
) {
  return () => {
    const { p, runId, busy, readOnly, onRespond, onDone } = handle.props;
    const budget = budgetPayload(p);
    const limit = limitPayload(p);
    const pause = limit ? undefined : watchdogPayload(p);
    const ciGate = ciGatePayload(p);
    const mergeGate = mergeGatePayload(p);
    const verdict = verdictGate(p);
    const interrupted = interruptedPayload(p);
    const repoConfig = repoConfigPayload(p);
    const github = githubTroublePayload(p);
    const sandbox = sandboxGatePayload(p);
    return budget ? (
      <BudgetPausePanel payload={budget} runId={runId} requestId={p.id} onDone={onDone} />
    ) : limit ? (
      <LimitPanel payload={limit} busy={busy} readOnly={readOnly} onRespond={onRespond} />
    ) : pause ? (
      <WatchdogPanel payload={pause} busy={busy} readOnly={readOnly} onRespond={onRespond} />
    ) : ciGate ? (
      <CIGatePanel payload={ciGate} busy={busy} readOnly={readOnly} onRespond={onRespond} />
    ) : mergeGate ? (
      <MergeGatePanel payload={mergeGate} busy={busy} readOnly={readOnly} onRespond={onRespond} />
    ) : verdict ? (
      <VerdictGatePanel payload={verdict} busy={busy} readOnly={readOnly} onRespond={onRespond} />
    ) : interrupted ? (
      <InterruptedPanel payload={interrupted} busy={busy} readOnly={readOnly} onRespond={onRespond} />
    ) : repoConfig ? (
      <RepoConfigGatePanel payload={repoConfig} busy={busy} readOnly={readOnly} onRespond={onRespond} />
    ) : github ? (
      <GitHubTroublePanel payload={github} busy={busy} readOnly={readOnly} onRespond={onRespond} />
    ) : sandbox ? (
      <SandboxGatePanel payload={sandbox} busy={busy} readOnly={readOnly} onRespond={onRespond} onDone={onDone} />
    ) : null;
  };
}

const textareaCss = css({ width: '100%', resize: 'vertical' });
const prReviewCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  borderRadius: t.rMd,
  border: `1px solid ${t.border}`,
  '& p': { margin: '0' },
  '& .what': { fontWeight: '500' },
  '& pre': { margin: '0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: t.textSm, maxHeight: '240px', overflow: 'auto' },
});
const chipsCss = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2, fontSize: t.textSm });

function PendingCard(
  handle: Handle<{
    p: PendingRequest;
    runId: string | undefined;
    diff: PayloadDiff | undefined;
    criteria: readonly CriterionRow[];
    /** A collaborator's change request on GitHub that arrived while this review waited (H12). */
    prReview?: PRReviewNote;
    onChanged: () => void;
  }>,
) {
  const s = services(handle);
  watch(handle, s.workspace);
  let mode: 'idle' | 'changes' = 'idle';
  let text = '';
  let state: 'idle' | 'sending' | 'sent' = 'idle';
  let error: Error | undefined;

  const respond = (decision: string, data?: unknown) => send((runId, id) => s.client.runRespond(runId, id, decision, data));
  // "Allow for the rest of this task" (P97): the core stores the rule, then approves this call
  const allowForTask = () => send((runId, id) => s.client.runApproveForTask(runId, id));
  const send = async (call: (runId: string, requestId: string) => Promise<unknown>) => {
    const { p, runId, onChanged } = handle.props;
    if (!runId) {
      error = new Error('This task has no run to answer.');
      void handle.update();
      return;
    }
    state = 'sending';
    error = undefined;
    void handle.update();
    try {
      await call(runId, p.id);
      // The answer is accepted; the request disappears when the core's events say so (it is not assumed here).
      state = 'sent';
      onChanged();
    } catch (e) {
      error = e instanceof Error ? e : new Error(String(e));
      state = 'idle';
    }
    void handle.update();
  };

  const setText = (v: string) => {
    text = v;
    void handle.update();
  };
  // a criterion's line added to the feedback (once), from its chip
  const addLine = (line: string) => {
    if (!line || text.split('\n').includes(line)) return;
    setText(text.trim() ? `${text.replace(/\s+$/, '')}\n${line}` : line);
  };

  return () => {
    const { p, runId, diff, criteria, prReview, onChanged } = handle.props;
    const info = payloadOf(p);
    const spec = specPayload(p);
    const permission = permissionPayload(p);
    const interrupted = interruptedPayload(p);
    const ro = s.workspace.license;
    const sending = state !== 'idle';
    const busy = sending || ro.readOnly;
    return (
      <div mix={cardCss} data-testid="pending-request" data-request-id={p.id} data-kind={p.spec.kind}>
        {/* an interrupted turn's engine title ("Approve to run it again ... or reject") does not match its panel's answers: a short question instead */}
        <h3>{interrupted ? `Run the ${interrupted.node ? visible(interrupted.node) + ' ' : ''}step again?` : visible(p.spec.title)}</h3>
        {/* the request's kind (review, question, approval) is its data-kind, not a line: the title says what is asked (P144) */}
        <div class="meta">
          {info.branch ? (
            <span style={{ display: 'inline-flex', alignItems: 'center', flexWrap: 'wrap', gap: '4px', minWidth: '0', maxWidth: '100%', overflowWrap: 'anywhere' }}>
              <Icon icon={GitBranch} size={13} />
              Branch <code>{visible(info.branch)}</code>
              {info.base ? (
                <>
                  {' '}
                  into <code>{visible(info.base)}</code>
                </>
              ) : null}
            </span>
          ) : null}
        </div>
        {info.files.length > 0 ? (
          <details open>
            <summary>
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: '4px', verticalAlign: 'middle' }}>
                <Icon icon={FileDiff} size={13} />
                {/* one row, 8 px between every part (the density rule); the spaces are for copying and reading aloud, flex does not draw them */}
                <span data-testid="review-diffstat" style={{ display: 'inline-flex', alignItems: 'baseline', gap: '8px', fontVariantNumeric: 'tabular-nums' }}>
                  <span>{plural(diff?.files || info.files.length, 'file')} changed</span>
                  {diff ? (
                    <>
                      {' '}
                      <span aria-hidden="true">·</span>{' '}
                      <span style={{ color: 'var(--success)' }}>+{diff.insertions}</span>{' '}
                      <span style={{ color: 'var(--danger)' }}>&minus;{diff.deletions}</span>
                    </>
                  ) : null}
                </span>
              </span>
            </summary>
            <ul>
              {info.files.map((f) => (
                <li key={f}>
                  <code>{visible(f)}</code>
                </li>
              ))}
            </ul>
            {/* the patch itself is the Diff section's (one viewer per page, the task's newest patch); the card points there (UI review P1-1) */}
            {diff ? (
              <div style={{ display: 'grid', gap: '8px', justifyItems: 'start' }}>
                {info.diff && info.diff.id < diff.id ? (
                  <p mix={muted} role="status" data-testid="review-diff-newer" style={{ margin: '0' }}>
                    A newer patch was recorded after this request. The counts and the Diff section are of the newer one.
                  </p>
                ) : null}
                <Button
                  size="sm"
                  variant="ghost"
                  mix={on('click', () => document.querySelector<HTMLElement>('[data-testid="task-diff"]')?.scrollIntoView({ block: 'start' }))}
                >
                  See the patch
                </Button>
              </div>
            ) : null}
          </details>
        ) : null}
        <ErrorNote error={error} what="Not sent" />
        <ReadOnlyNote />
        {state === 'sent' ? (
          <p role="status" mix={muted}>
            Answer sent. Waiting for the core to confirm...
          </p>
        ) : null}

        {permission ? (
          <PermissionPromptPanel payload={permission} busy={sending} readOnly={ro.readOnly} onRespond={respond} onAllowForTask={allowForTask} />
        ) : hasGatePanel(p) ? (
          <GateAnswer p={p} runId={runId} busy={sending} readOnly={ro.readOnly} onRespond={respond} onDone={onChanged} />
        ) : spec ? (
          <SpecGate payload={spec} busy={sending} readOnly={ro.readOnly} onRespond={respond} />
        ) : p.spec.kind === 'question' ? (
          <>
            <Field label="Your answer">
              {(id) => <textarea id={id} rows={3} value={text} disabled={busy} mix={[textareaCss, on('input', (e) => setText(e.currentTarget.value))]} />}
            </Field>
            <div mix={formActions}>
              <Button variant="primary" disabled={busy || !text.trim()} mix={on('click', () => void respond('answered', { text }))}>
                Send answer
              </Button>
            </div>
          </>
        ) : p.spec.kind === 'review' ? (
          mode === 'idle' ? (
            <>
              {prReview ? (
                <div mix={prReviewCss} data-testid="review-pr-changes">
                  <p class="what">{visible(prReview.summary)}</p>
                  <p mix={muted}>Send back sends the worker these words:</p>
                  <pre>{visible(prReview.feedback)}</pre>
                  <div mix={formActions}>
                    <Button variant="primary" disabled={busy} mix={on('click', () => void respond('rejected', { feedback: prReview.feedback }))}>
                      Send back with the GitHub review
                    </Button>
                  </div>
                </div>
              ) : null}
              <div mix={formActions}>
                <Button variant="primary" disabled={busy} mix={on('click', () => void respond('approved'))}>
                  Approve
                </Button>
                <Button
                  disabled={sending}
                  mix={on('click', () => {
                    mode = 'changes';
                    // start from the criteria still to meet, so the person edits instead of retyping them
                    if (!text.trim()) text = changesDraft(criteria);
                    void handle.update();
                  })}
                >
                  Request changes
                </Button>
              </div>
            </>
          ) : (
            <>
              <Field label="Feedback for the worker">
                {(id) => <textarea id={id} rows={4} value={text} disabled={sending} mix={[textareaCss, on('input', (e) => setText(e.currentTarget.value))]} />}
              </Field>
              {criteria.some((c) => changeLine(c)) ? (
                <div mix={chipsCss} data-testid="changes-criteria">
                  <span mix={muted}>Add a criterion:</span>
                  {criteria
                    .filter((c) => changeLine(c))
                    .map((c) => (
                      <Button key={c.index} size="sm" variant="ghost" disabled={sending} title={c.text} mix={on('click', () => addLine(changeLine(c)))}>
                        {c.id || `Criterion ${c.index + 1}`}
                      </Button>
                    ))}
                </div>
              ) : null}
              <div mix={formActions}>
                {/* an empty send is allowed: the core tells the worker it was sent back without comment */}
                <Button variant="primary" disabled={sending} mix={on('click', () => void respond('rejected', { feedback: text.trim() }))}>
                  {text.trim() ? 'Send back with these points' : 'Send back without comment'}
                </Button>
                <Button
                  disabled={sending}
                  mix={on('click', () => {
                    mode = 'idle';
                    void handle.update();
                  })}
                >
                  Back
                </Button>
              </div>
            </>
          )
        ) : (
          <div mix={formActions}>
            <Button variant="primary" disabled={busy} mix={on('click', () => void respond('approved'))}>
              Approve
            </Button>
            <Button disabled={sending} mix={on('click', () => void respond('rejected'))}>
              Reject
            </Button>
          </div>
        )}
      </div>
    );
  };
}

const gateCss = css({
  display: 'grid',
  gap: t.s3,
  minWidth: '0',
  '& h4': { fontSize: t.textMd, fontWeight: '600' },
  '& .spec': { padding: `${t.s2} ${t.s3}`, borderRadius: t.rMd, border: `1px solid ${t.border}`, background: t.bgApp, maxHeight: '420px', overflow: 'auto' },
  '& ol': { margin: '0', paddingLeft: '22px', display: 'grid', gap: '4px' },
  '& ol strong': { marginRight: '6px', fontFamily: t.mono, fontSize: t.textSm },
});

/**
 * The spec gate: the PM agent's spec and its Given/When/Then criteria. Nothing is built until you accept. The spec is agent
 * output (untrusted) and is shown inert. You can edit the criteria before accepting (sent as data.criteria) or send the spec
 * back with feedback. `busy` is a request in flight; `readOnly` (license) turns off approving only: sending a spec back stays
 * available.
 */
export function SpecGate(handle: Handle<{ payload: SpecReviewPayload; busy: boolean; readOnly?: boolean; onRespond: (decision: string, data?: unknown) => Promise<void> }>) {
  let lines: string | undefined;
  let rejecting = false;
  let feedback = '';
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  return () => {
    const { payload, busy, readOnly = false, onRespond } = handle.props;
    const criteria = payload.criteria ?? [];
    lines ??= criteria.map(criterionLine).join('\n');
    const edited = editedCriteria(criteria, lines);
    return (
      <div mix={gateCss} data-testid="spec-gate">
        <h4>Spec: {payload.title}</h4>
        <p mix={muted}>Written by the PM agent, so treat it as a proposal. Accepting it fixes the acceptance criteria that tests are written against.</p>
        <details open>
          <summary>Spec text</summary>
          <div class="spec">
            <Markdown source={payload.spec} maxChars={40000} />
          </div>
        </details>
        <h4>Acceptance criteria (Given / When / Then)</h4>
        <ol data-testid="spec-criteria">
          {criteria.map((c) => (
            <li key={c.id}>
              <strong>{c.id}</strong> Given {c.given}; when {c.when}; then {c.then}
            </li>
          ))}
        </ol>
        <Field label="Edit the criteria before accepting (one per line)">
          {(id) => (
            <textarea
              id={id}
              aria-label="Edit criteria"
              rows={Math.max(3, criteria.length + 1)}
              spellcheck={false}
              value={lines}
              mix={[textareaCss, on('input', (e) => set(() => (lines = e.currentTarget.value)))]}
            />
          )}
        </Field>
        {rejecting ? (
          <>
            <Field label="Feedback for the PM agent">
              {(id) => <textarea id={id} rows={3} value={feedback} mix={[textareaCss, on('input', (e) => set(() => (feedback = e.currentTarget.value)))]} />}
            </Field>
            <div mix={formActions}>
              <Button variant="primary" disabled={busy || !feedback.trim()} mix={on('click', () => void onRespond('rejected', { feedback: feedback.trim() }))}>
                Send spec back
              </Button>
              <Button mix={on('click', () => set(() => (rejecting = false)))}>Back</Button>
            </div>
          </>
        ) : (
          <div mix={formActions}>
            <Button variant="primary" disabled={busy || readOnly} mix={on('click', () => void onRespond('approved', edited ? { criteria: edited } : undefined))}>
              {edited ? 'Accept spec with my edited criteria' : 'Accept spec'}
            </Button>
            <Button disabled={busy} mix={on('click', () => set(() => (rejecting = true)))}>
              Reject with feedback
            </Button>
          </div>
        )}
        {edited ? <Badge tone="info">criteria edited</Badge> : null}
      </div>
    );
  };
}

/**
 * The calls the person allowed for the rest of this task (P97), each with Stop allowing: the next such call asks again. They end
 * with the task by themselves. Renders nothing when there are none.
 */
export function AllowRules(handle: Handle<{ rules: TaskAllowRule[]; onChanged: () => void }>) {
  const s = services(handle);
  let busy = '';
  let error: Error | undefined;
  const stop = async (id: string) => {
    busy = id;
    error = undefined;
    void handle.update();
    try {
      await s.client.approvalRuleRemove(id);
      handle.props.onChanged();
    } catch (e) {
      error = e instanceof Error ? e : new Error(String(e));
    }
    busy = '';
    void handle.update();
  };
  return () => {
    const { rules } = handle.props;
    if (rules.length === 0) return null;
    return (
      <section mix={cardCss} aria-label="Allowed for the rest of this task" data-testid="allow-rules">
        <h3>Allowed for the rest of this task</h3>
        <p mix={muted} style={{ margin: '0' }}>
          These calls run without asking you until the task ends. Any other call still asks.
        </p>
        <ErrorNote error={error} what="Not stopped" />
        <ul style={{ display: 'grid', gap: '6px', margin: '0', padding: '0', listStyle: 'none' }}>
          {rules.map((r) => (
            <li key={r.id} data-testid="allow-rule" style={{ display: 'flex', alignItems: 'center', gap: '8px', minWidth: '0' }}>
              <code style={{ flex: '1', minWidth: '0', overflowWrap: 'anywhere' }}>{visible(r.summary)}</code>
              <Button size="sm" disabled={busy !== ''} mix={on('click', () => void stop(r.id))}>
                {busy === r.id ? 'Stopping...' : 'Stop allowing'}
              </Button>
            </li>
          ))}
        </ul>
      </section>
    );
  };
}
