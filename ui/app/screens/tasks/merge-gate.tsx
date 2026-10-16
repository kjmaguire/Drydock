// The two questions a pull request task asks before its merge (docs/github.md sections 3 and 5), answered on the task page:
// - the CI gate (payload gate "ci"): no CI reported on the pull request, a check its branch protection requires never did,
//   or checks were still running at the deadline. Check again (answered), merge without CI this time (approved with
//   {waive_ci: true}; only a person can, and it never covers a failed check), or stop (rejected).
// - the merge gate (payload gate "merge"): GitHub would not merge the pull request now (branch protection, behind its base,
//   conflicts, a draft, not worked out yet). Check again or stop. Drydock never bypasses branch protection.
// Check names come from GitHub and are shown as plain text.
//
// And the question a blocking gate asks when none of its checks could judge (payload gate "verdict", audit 2026-10-16, M28):
// the verification run or a security scan gate ended with every check skipped or unable to run, so passing it would send the work
// on as verified when nothing verified it. Check again (answered), go on without a result this time (approved with
// {waive_verdict: true}: only for this exact content, recorded with the person's name, after a second click), or stop
// (rejected, after a second click).

import { css, on, ref, type Handle } from 'remix/component';
import { ExternalLink, Octagon, RefreshCw, ShieldOff } from 'lucide';
import { isRecord } from '../../api/guards.ts';
import type { PendingRequest } from '../../api/types.ts';
import { visible } from '../../lib/visible.ts';
import { Button } from '../../ui/button.tsx';
import { formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { safeHref } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';

/** pipeline.ciQuestion's payload. */
export interface CIGatePayload {
  gate: 'ci';
  pr: number;
  url?: string;
  head: string;
  reason: 'none' | 'missing' | 'running' | string;
  missing?: string[] | null;
  running?: string[] | null;
  /** What Drydock could not see or did not accept (an agent's waiver, unreadable branch protection). */
  notes?: string[] | null;
}

/** The merge step's payload when GitHub would not merge (pipeline.MergePR). */
export interface MergeGatePayload {
  gate: 'merge';
  pr: number;
  url?: string;
  state: string;
  reason: string;
}

/** One check that gave no verdict, as the core lists it (pipeline.unjudged). */
export interface UnjudgedCheck {
  name: string;
  /** "skipped" or "error" as the core sends them; any other text is "unknown" here. */
  status: 'skipped' | 'error' | 'unknown';
  reason: string;
}

/** The no-verdict question's payload (pipeline.noVerdict, gate "verdict"), every field checked. */
export interface VerdictGatePayload {
  gate: 'verdict';
  /** The workflow node that asked. */
  node: string;
  /** What was being judged, in a few words: "the checks of this task", "the security scans". */
  what: string;
  /** The fingerprint of the content the question is about (the waiver covers this content only). */
  diff: string;
  checks: UnjudgedCheck[];
  /** What the core adds, for example that a check was run again because only a person can decide this. */
  notes: string[];
}

const textOf = (v: unknown): string => (typeof v === 'string' ? v : '');

/** A check the core listed, or nothing for an entry that is not one (a check without a name cannot be shown). */
function unjudgedCheck(c: unknown): UnjudgedCheck[] {
  if (!isRecord(c) || typeof c.name !== 'string' || c.name.trim() === '') return [];
  return [{ name: c.name, status: c.status === 'skipped' || c.status === 'error' ? c.status : 'unknown', reason: textOf(c.reason) }];
}

/**
 * The no-verdict question's payload, only for a question of the gate "verdict". Everything is checked: a missing or mistyped
 * `checks` or `notes` is an empty list, a check without a name is dropped, a field of another type is empty.
 */
export function verdictGate(p: PendingRequest): VerdictGatePayload | undefined {
  const v = p.spec.payload;
  if (p.spec.kind !== 'question' || !isRecord(v) || v.gate !== 'verdict') return undefined;
  return {
    gate: 'verdict',
    node: textOf(v.node),
    what: textOf(v.what),
    diff: textOf(v.diff),
    checks: Array.isArray(v.checks) ? v.checks.flatMap(unjudgedCheck) : [],
    notes: Array.isArray(v.notes) ? v.notes.filter((n): n is string => typeof n === 'string') : [],
  };
}

/** One line of "What was not checked": the check by its name, what happened to it and why, as the core worded it. */
export function uncheckedLine(c: UnjudgedCheck): string {
  const what = c.status === 'skipped' ? 'was skipped' : c.status === 'error' ? 'could not run' : 'gave no result';
  const reason = visible(c.reason).trim();
  return `${visible(c.name)} ${what}${reason ? `: ${reason}` : '.'}`;
}

export function ciGatePayload(p: PendingRequest): CIGatePayload | undefined {
  const v = p.spec.payload as Partial<CIGatePayload> | undefined;
  return v && v.gate === 'ci' && p.spec.kind === 'question' ? (v as CIGatePayload) : undefined;
}

export function mergeGatePayload(p: PendingRequest): MergeGatePayload | undefined {
  const v = p.spec.payload as Partial<MergeGatePayload> | undefined;
  return v && v.gate === 'merge' && p.spec.kind === 'question' ? (v as MergeGatePayload) : undefined;
}

const CI_REASON: Record<string, string> = {
  none: 'No check run and no commit status reported on the reviewed commit. Merging now would merge code that no CI tested.',
  missing: 'Some checks reported, but not every check the branch protection requires.',
  running: 'CI started but did not finish in time.',
};

const MERGE_HINT: Record<string, string> = {
  blocked: 'A required review or check is missing or has not passed. The pull request on GitHub shows which.',
  behind: 'The base branch moved on and its protection wants the branch up to date first. A commit added to the branch on GitHub was not reviewed, so Drydock will not merge it unseen.',
  dirty: 'The branch conflicts with the base branch.',
  draft: 'Mark the pull request ready for review on GitHub.',
  unknown: 'GitHub was still working out whether it can merge; checking again usually settles it.',
};

const gateCss = css({
  display: 'grid',
  gap: t.s3,
  minWidth: '0',
  '& p': { margin: '0' },
  '& .what': { fontWeight: '500' },
  '& .checks, & .notes': { margin: '0', paddingLeft: '20px', fontSize: t.textSm, color: t.textMuted },
  '& .link': { display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: t.textSm },
});

type Respond = (decision: string, data?: unknown) => Promise<void>;

function GitHubLink(handle: Handle<{ url?: string; pr: number }>) {
  return () => {
    const href = safeHref(handle.props.url ?? '');
    return href ? (
      <a class="link" href={href} target="_blank" rel="noopener noreferrer" data-rmx-document>
        Open pull request #{handle.props.pr} on GitHub
        <Icon icon={ExternalLink} size={12} />
      </a>
    ) : null;
  };
}

/** The CI gate: check again, merge without CI this time, or stop. */
export function CIGatePanel(handle: Handle<{ payload: CIGatePayload; busy: boolean; readOnly: boolean; onRespond: Respond }>) {
  return () => {
    const { payload: p, busy, readOnly, onRespond } = handle.props;
    const missing = p.missing ?? [];
    const running = p.running ?? [];
    const notes = p.notes ?? [];
    return (
      <div mix={gateCss} data-testid="ci-gate" data-reason={p.reason}>
        <p class="what">{CI_REASON[p.reason] ?? 'CI gave no answer for the reviewed commit.'}</p>
        {missing.length > 0 ? (
          <ul class="checks" aria-label="Required checks that did not report">
            {missing.map((n) => (
              <li key={n}>{visible(n)}</li>
            ))}
          </ul>
        ) : null}
        {running.length > 0 ? (
          <ul class="checks" aria-label="Checks still running">
            {running.map((n) => (
              <li key={n}>{visible(n)}</li>
            ))}
          </ul>
        ) : null}
        {notes.length > 0 ? (
          <ul class="notes" data-testid="ci-gate-notes">
            {notes.map((n) => (
              <li key={n}>{visible(n)}</li>
            ))}
          </ul>
        ) : null}
        <GitHubLink url={p.url} pr={p.pr} />
        <p>Merging without CI applies to this commit only and is recorded with your name; a check that fails is still sent back to the worker.</p>
        <div mix={formActions}>
          <Button variant="primary" icon={RefreshCw} disabled={busy || readOnly} mix={on('click', () => void onRespond('answered', {}))}>
            Check CI again
          </Button>
          <Button icon={ShieldOff} disabled={busy || readOnly} mix={on('click', () => void onRespond('approved', { waive_ci: true }))}>
            Merge without CI this time
          </Button>
          <Button variant="danger" icon={Octagon} disabled={busy} mix={on('click', () => void onRespond('rejected'))}>
            Stop
          </Button>
        </div>
      </div>
    );
  };
}

/** The merge gate: GitHub would not merge now. Check again or stop. */
export function MergeGatePanel(handle: Handle<{ payload: MergeGatePayload; busy: boolean; readOnly: boolean; onRespond: Respond }>) {
  return () => {
    const { payload: p, busy, readOnly, onRespond } = handle.props;
    return (
      <div mix={gateCss} data-testid="merge-gate" data-state={p.state}>
        <p class="what">{MERGE_HINT[p.state] ?? visible(p.reason)}</p>
        <GitHubLink url={p.url} pr={p.pr} />
        <p>Drydock never asks GitHub to bypass branch protection. Fix the cause on GitHub, then check again.</p>
        <div mix={formActions}>
          <Button variant="primary" icon={RefreshCw} disabled={busy || readOnly} mix={on('click', () => void onRespond('answered', {}))}>
            Check again
          </Button>
          <Button variant="danger" icon={Octagon} disabled={busy} mix={on('click', () => void onRespond('rejected'))}>
            Stop
          </Button>
        </div>
      </div>
    );
  };
}

const optionsCss = css({
  display: 'grid',
  gap: t.s3,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': { display: 'grid', gap: '4px', justifyItems: 'start', minWidth: '0' },
  '& .why': { color: t.textMuted, fontSize: t.textSm },
});
const confirmCss = css({
  display: 'grid',
  gap: t.s2,
  padding: t.s3,
  borderRadius: t.rMd,
  border: `1px solid ${t.border}`,
  background: t.bgApp,
  '& p': { margin: '0', overflowWrap: 'anywhere' },
});

type Asking = 'waive' | 'stop';

/**
 * The no-verdict question: what was not checked and why, then Check again, Go on without a result this time, or Stop, each with
 * a sentence on what it does. Going on without a result and stopping are asked a second time, in words that name what happens;
 * "Go back" leaves the question as it was. Read-only (license) turns off checking again and going on, not stopping.
 */
export function VerdictGatePanel(handle: Handle<{ payload: VerdictGatePayload; busy: boolean; readOnly: boolean; onRespond: Respond }>) {
  const ids = `verdict-${handle.id}`;
  let asking: Asking | undefined;
  let waiveBtn: HTMLElement | undefined;
  let stopBtn: HTMLElement | undefined;
  // an answer is on its way until onRespond returns: a second click in the meantime sends nothing (the card's `busy` follows a render later)
  let answering = false;

  const send = async (decision: string, data?: unknown) => {
    if (answering) return;
    answering = true;
    try {
      await handle.props.onRespond(decision, data);
    } finally {
      answering = false;
    }
  };
  const ask = (what: Asking) => {
    asking = what;
    void handle.update();
  };
  const back = async () => {
    const from = asking;
    asking = undefined;
    await handle.update();
    (from === 'waive' ? waiveBtn : stopBtn)?.focus(); // focus goes back to the button that asked
  };

  return () => {
    const { payload: p, busy, readOnly } = handle.props;
    const what = visible(p.what).trim() || 'this work';
    const sentence = asking === 'waive' ? `Go on without a result for ${what}? It applies to this exact content only, and it is recorded with your name.` : 'Stop the run? It ends here.';
    return (
      <div mix={gateCss} data-testid="verdict-gate">
        <p class="what" id={`${ids}-what`}>
          What was not checked:
        </p>
        {p.checks.length > 0 ? (
          <ul class="checks" aria-labelledby={`${ids}-what`} data-testid="verdict-gate-checks">
            {p.checks.map((c, i) => (
              <li key={`${i}:${c.name}`}>{uncheckedLine(c)}</li>
            ))}
          </ul>
        ) : (
          <p class="checks" data-testid="verdict-gate-checks">
            Nothing was there to check.
          </p>
        )}
        {p.notes.length > 0 ? (
          <ul class="notes" data-testid="verdict-gate-notes">
            {p.notes.map((n, i) => (
              <li key={i}>{visible(n)}</li>
            ))}
          </ul>
        ) : null}
        {asking === undefined ? (
          <ul mix={optionsCss} aria-label="What you can do" data-testid="verdict-gate-options">
            <li>
              <Button variant="primary" icon={RefreshCw} disabled={busy || readOnly} aria-describedby={`${ids}-again`} mix={on('click', () => void send('answered', {}))}>
                Check again
              </Button>
              <span class="why" id={`${ids}-again`}>
                Use this after you installed the tool or fixed the setting.
              </span>
            </li>
            <li>
              <Button icon={ShieldOff} disabled={busy || readOnly} aria-describedby={`${ids}-waive`} mix={[ref((n) => (waiveBtn = n as HTMLElement)), on('click', () => ask('waive'))]}>
                Go on without a result this time
              </Button>
              <span class="why" id={`${ids}-waive`}>
                The work moves on as if it had been checked, for this exact content only. This is recorded.
              </span>
            </li>
            <li>
              <Button variant="danger" icon={Octagon} disabled={busy} aria-describedby={`${ids}-stop`} mix={[ref((n) => (stopBtn = n as HTMLElement)), on('click', () => ask('stop'))]}>
                Stop
              </Button>
              <span class="why" id={`${ids}-stop`}>
                Ends the run.
              </span>
            </li>
          </ul>
        ) : (
          <div mix={confirmCss} role="group" aria-label={sentence} data-testid="verdict-confirm" data-asking={asking}>
            <p>{sentence}</p>
            <div mix={formActions}>
              <Button
                variant="danger"
                disabled={busy || (asking === 'waive' && readOnly)}
                mix={on('click', () => void (asking === 'waive' ? send('approved', { waive_verdict: true }) : send('rejected')))}
              >
                {asking === 'waive' ? 'Yes, go on without a result' : 'Yes, stop the run'}
              </Button>
              <Button mix={[ref((n) => (n as HTMLElement).focus()), on('click', () => void back())]}>Go back</Button>
            </div>
          </div>
        )}
      </div>
    );
  };
}
