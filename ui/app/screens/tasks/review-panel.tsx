// The human decision panel: every pending request of the task's run, each answered by buttons bound to the request id (never
// to message text). Spec reviews get the spec gate, budget pauses the resume form, questions a free-text answer, reviews
// Approve / Request changes, the limit question and watchdog pauses Continue (with guidance) / Stop, the pull request's CI
// gate Check CI again / Merge without CI this time / Stop and its merge gate Check again / Stop, a changed repository git
// configuration (gate repo_config_changed) its changed items with Trust and continue / Stop the run, a worker's permission
// request (gate permission_prompt) the tool and its exact input with Allow once / Refuse, anything else Approve / Reject. Read-only (license) turns off approving and answering; refusing
// work (reject, request changes, send the spec back) stays available.

import { css, on, type Handle } from 'remix/component';
import { FileDiff, GitBranch, Hand } from 'lucide';
import type { SpecReviewPayload } from '../../api/extTypes.ts';
import type { PendingRequest, TaskDetail } from '../../api/types.ts';
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
import { PermissionPromptPanel, permissionPayload } from '../shared/permission-prompt.tsx';
import { RepoConfigGatePanel, repoConfigPayload } from '../shared/repo-config.tsx';
import { CIGatePanel, ciGatePayload, MergeGatePanel, mergeGatePayload } from './merge-gate.tsx';
import { budgetPayload, criterionLine, editedCriteria, payloadOf, specPayload } from './tasks.ts';

// The decide card (Crew HQ, decision P64): a soft yellow card with the requests as white cards inside; the primary answer is
// the black pill.
const panelCss = css({
  display: 'grid',
  gap: t.s3,
  padding: '18px',
  borderRadius: t.rXl,
  border: `1px solid ${t.mentionBorder}`,
  background: t.mentionBg,
  '& > h2': { display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textLg, fontWeight: '650', letterSpacing: '-0.01em' },
  '& > h2 svg': { color: t.warning },
});

const cardCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  borderRadius: t.rLg,
  border: `1px solid ${t.border}`,
  background: t.bgSurface,
  minWidth: '0',
  '& > h3': { fontSize: t.textMd, fontWeight: '600', overflowWrap: 'anywhere' },
  '& .meta': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, color: t.textMuted, fontSize: t.textSm },
  '& .meta svg': { color: t.textFaint },
  '& details ul': { margin: `${t.s2} 0 0`, paddingLeft: '20px', fontSize: t.textSm },
  '& summary': { cursor: 'pointer', fontSize: t.textSm, color: t.textMuted },
});

/** The pending requests of the task's run. Renders nothing when nothing is pending. */
export function ReviewPanel(handle: Handle<{ detail: TaskDetail; onChanged: () => void }>) {
  return () => {
    const { detail, onChanged } = handle.props;
    if (detail.pending.length === 0) return null;
    return (
      <section mix={panelCss} aria-label="Needs your decision" data-testid="review-panel">
        <h2>
          <Icon icon={Hand} size={16} />
          Needs your decision
        </h2>
        {detail.pending.map((p) => (
          <PendingCard key={p.id} p={p} runId={detail.run?.id} onChanged={onChanged} />
        ))}
      </section>
    );
  };
}

const textareaCss = css({ width: '100%', resize: 'vertical' });

function PendingCard(handle: Handle<{ p: PendingRequest; runId: string | undefined; onChanged: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  let mode: 'idle' | 'changes' = 'idle';
  let text = '';
  let state: 'idle' | 'sending' | 'sent' = 'idle';
  let error: Error | undefined;

  const respond = async (decision: string, data?: unknown) => {
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
      await s.client.runRespond(runId, p.id, decision, data);
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

  return () => {
    const { p, runId, onChanged } = handle.props;
    const info = payloadOf(p);
    const spec = specPayload(p);
    const budget = budgetPayload(p);
    const limit = limitPayload(p);
    const pause = limit ? undefined : watchdogPayload(p);
    const ciGate = ciGatePayload(p);
    const mergeGate = mergeGatePayload(p);
    const repoConfig = repoConfigPayload(p);
    const permission = permissionPayload(p);
    const ro = s.workspace.license;
    const sending = state !== 'idle';
    const busy = sending || ro.readOnly;
    return (
      <div mix={cardCss} data-testid="pending-request" data-request-id={p.id} data-kind={p.spec.kind}>
        <h3>{visible(p.spec.title)}</h3>
        <div class="meta">
          <span>
            Request kind: <code>{p.spec.kind}</code>
          </span>
          {info.branch ? (
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
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
                {info.files.length} file(s) changed
              </span>
            </summary>
            <ul>
              {info.files.map((f) => (
                <li key={f}>
                  <code>{visible(f)}</code>
                </li>
              ))}
            </ul>
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
          <PermissionPromptPanel payload={permission} busy={sending} readOnly={ro.readOnly} onRespond={respond} />
        ) : budget ? (
          <BudgetPausePanel payload={budget} runId={runId} requestId={p.id} onDone={onChanged} />
        ) : limit ? (
          <LimitPanel payload={limit} busy={sending} readOnly={ro.readOnly} onRespond={respond} />
        ) : pause ? (
          <WatchdogPanel payload={pause} busy={sending} readOnly={ro.readOnly} onRespond={respond} />
        ) : ciGate ? (
          <CIGatePanel payload={ciGate} busy={sending} readOnly={ro.readOnly} onRespond={respond} />
        ) : mergeGate ? (
          <MergeGatePanel payload={mergeGate} busy={sending} readOnly={ro.readOnly} onRespond={respond} />
        ) : repoConfig ? (
          <RepoConfigGatePanel payload={repoConfig} busy={sending} readOnly={ro.readOnly} onRespond={respond} />
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
            <div mix={formActions}>
              <Button variant="primary" disabled={busy} mix={on('click', () => void respond('approved'))}>
                Approve
              </Button>
              <Button
                disabled={sending}
                mix={on('click', () => {
                  mode = 'changes';
                  void handle.update();
                })}
              >
                Request changes
              </Button>
            </div>
          ) : (
            <>
              <Field label="Feedback for the worker">
                {(id) => <textarea id={id} rows={3} value={text} disabled={sending} mix={[textareaCss, on('input', (e) => setText(e.currentTarget.value))]} />}
              </Field>
              <div mix={formActions}>
                <Button variant="primary" disabled={sending || !text.trim()} mix={on('click', () => void respond('rejected', { feedback: text.trim() }))}>
                  Send feedback
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
