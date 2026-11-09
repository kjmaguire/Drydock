// How a merged task closes (critique 2026-11-08): at the top of its page, where the decision card was, one calm line says the
// change landed and where ("Merged into main as 1a2b3c4"), who approved it and when, with what it was decided on (the change's
// size, the checks, the reviewer's verdict) and the change itself one click away in the dock's Diff. The task page used to end
// on a disabled steer box; this is its last word instead. Nothing here acts on the repository.

import { css, on, type Handle } from 'remix/component';
import { FileDiff, GitMerge } from 'lucide';
import type { Agent, SummaryCheck, TaskDetail } from '../../api/types.ts';
import { agentName, plural } from '../../lib/format.ts';
import { visible } from '../../lib/visible.ts';
import { services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { DecisionFacts } from './review-panel.tsx';
import { checksFact, mergedTitle, personName, reviewerFact, shortSha, type Fact } from './task-page.ts';
import { diffText, newestDiffStat } from './tasks.ts';

const cardCss = css({
  display: 'grid',
  gridTemplateColumns: 'auto minmax(0, 1fr)',
  columnGap: t.s3,
  rowGap: t.s2,
  alignItems: 'start',
  padding: t.s4,
  borderRadius: t.rLg,
  border: `1px solid color-mix(in srgb, ${t.success} 35%, transparent)`,
  background: t.successSoft,
  minWidth: '0',
  '& > .mark': {
    gridRow: 'span 3',
    display: 'grid',
    placeItems: 'center',
    width: '28px',
    height: '28px',
    borderRadius: '50%',
    color: t.onAccent,
    background: t.successSolid,
    // the one moment the app marks: the mark settles in once, from already visible (reduced motion shortens it to nothing)
    animation: 'dd-merged 420ms cubic-bezier(0.16, 1, 0.3, 1) both',
  },
  '@keyframes dd-merged': { from: { transform: 'scale(0.7)', opacity: '0.4' }, to: { transform: 'none', opacity: '1' } },
  '& > h2': { margin: '0', fontSize: t.textMd, fontWeight: '600', lineHeight: '28px', overflowWrap: 'anywhere' },
  '& > h2 code': { fontSize: t.textSm, fontWeight: '500', marginLeft: '6px' },
  '& > .who': { margin: '0', color: t.textMuted, fontSize: t.textSm, fontVariantNumeric: 'tabular-nums' },
  '& > .acts': { display: 'flex', flexWrap: 'wrap', gap: t.s2, marginTop: t.s1 },
});

/** When a stamp was: "today at 10:35 PM", "on 3 Nov at 10:35 PM". */
function when(ms: number, now = Date.now()): string {
  const d = new Date(ms);
  const time = d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
  return new Date(now).toDateString() === d.toDateString() ? `today at ${time}` : `on ${d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })} at ${time}`;
}

/** Renders nothing unless the task is completed and the core recorded its merge. */
export function MergedCard(handle: Handle<{ detail: TaskDetail; checks: readonly SummaryCheck[]; agents: readonly Agent[] }>) {
  const s = services(handle);
  return () => {
    const { detail: d, checks, agents } = handle.props;
    const m = d.merged;
    if (d.task.status !== 'COMPLETED' || !m) return null;
    const diff = newestDiffStat(d.evidence);
    const reviewer = reviewerFact(d.reviews, (id) => agentName(id, agents));
    const facts: Fact[] = [
      ...(diff ? [{ text: `${plural(diff.files, 'file')} ${diffText(diff)}`, tone: 'muted' as const, kind: 'change' as const }] : []),
      ...(checks.length ? [checksFact(checks)] : []),
      ...(reviewer ? [reviewer] : []),
    ];
    const by = d.task.acceptedBy ? personName(d.task.acceptedBy) : '';
    const sha = shortSha(m.commit);
    return (
      <section mix={cardCss} aria-label="Merged" data-testid="task-merged">
        <span class="mark" aria-hidden="true">
          <Icon icon={GitMerge} size={15} />
        </span>
        <h2>
          {mergedTitle(m)}
          {sha ? (
            <>
              {' '}
              as <code title={visible(m.commit ?? '')}>{sha}</code>
            </>
          ) : null}
        </h2>
        <p class="who">{[by ? `Approved by ${visible(by)}` : 'Approved', m.at ? when(m.at) : ''].filter(Boolean).join(' ')}</p>
        {facts.length ? <DecisionFacts facts={facts} /> : null}
        <div class="acts" style={{ gridColumn: '2' }}>
          <Button size="sm" icon={FileDiff} mix={on('click', () => s.ui.openDiff(d.task.id))}>
            See what changed
          </Button>
        </div>
      </section>
    );
  };
}
