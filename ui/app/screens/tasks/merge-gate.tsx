// The two questions a pull request task asks before its merge (docs/github.md sections 3 and 5), answered on the task page:
// - the CI gate (payload gate "ci"): no CI reported on the pull request, a check its branch protection requires never did,
//   or checks were still running at the deadline. Check again (answered), merge without CI this time (approved with
//   {waive_ci: true}; only a person can, and it never covers a failed check), or stop (rejected).
// - the merge gate (payload gate "merge"): GitHub would not merge the pull request now (branch protection, behind its base,
//   conflicts, a draft, not worked out yet). Check again or stop. Drydock never bypasses branch protection.
// Check names come from GitHub and are shown as plain text.

import { css, on, type Handle } from 'remix/component';
import { ExternalLink, Octagon, RefreshCw, ShieldOff } from 'lucide';
import type { PendingRequest } from '../../api/types.ts';
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
              <li key={n}>{n}</li>
            ))}
          </ul>
        ) : null}
        {running.length > 0 ? (
          <ul class="checks" aria-label="Checks still running">
            {running.map((n) => (
              <li key={n}>{n}</li>
            ))}
          </ul>
        ) : null}
        {notes.length > 0 ? (
          <ul class="notes" data-testid="ci-gate-notes">
            {notes.map((n) => (
              <li key={n}>{n}</li>
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
        <p class="what">{MERGE_HINT[p.state] ?? p.reason}</p>
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
