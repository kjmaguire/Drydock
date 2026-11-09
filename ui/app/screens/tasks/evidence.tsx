// Evidence and reviewer verdicts on the task page. Summaries and feedback are agent output: shown as text or inert Markdown.

import { css, type Handle } from 'remix/component';
import { CircleCheck, CircleDashed, CircleX, Info } from 'lucide';
import type { Agent, Evidence, ReviewRecord } from '../../api/types.ts';
import { agentName } from '../../lib/format.ts';
import { Avatar, Badge, muted } from '../../ui/data.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Markdown, safeHref } from '../../ui/markdown.tsx';
import { t, tone, type Tone } from '../../ui/tokens.ts';
import { evidenceLabel, evidenceResult, RESULT_TEXT, type EvidenceResult } from './tasks.ts';

const listCss = css({
  display: 'grid',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  '& > li': {
    display: 'grid',
    gridTemplateColumns: '20px 1fr',
    gap: `2px ${t.s3}`,
    padding: `10px ${t.s4}`,
    borderTop: `1px solid ${t.border}`,
    minWidth: '0',
  },
  '& > li:first-child': { borderTop: '0' },
  '& .head': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, minWidth: '0' },
  '& .body': { gridColumn: '2', color: t.textMuted, fontSize: t.textSm, overflowWrap: 'anywhere' },
  '& .icon': { display: 'inline-flex', paddingTop: '2px' },
});

const RESULT_TONE: Record<EvidenceResult, Tone> = { pass: 'ok', fail: 'err', skipped: 'muted', info: 'muted' };
const RESULT_ICON = { pass: CircleCheck, fail: CircleX, skipped: CircleDashed, info: Info };

/** What the run recorded as proof: pass, fail, skipped (never counted as a pass) or info, with plain labels. */
export function EvidenceList(handle: Handle<{ evidence: readonly Evidence[] }>) {
  return () => {
    const evidence = handle.props.evidence;
    if (evidence.length === 0) return <p mix={muted}>No evidence yet. A task enters review only with evidence.</p>;
    const runs = new Set(evidence.map((e) => e.attempt)).size;
    return (
      <ul mix={listCss} aria-label="Evidence">
        {evidence.map((e) => {
          const res = evidenceResult(e);
          const link = e.kind === 'preview' && e.ref ? safeHref(e.ref) : undefined;
          const tn = RESULT_TONE[res];
          return (
            <li key={e.id} data-testid="evidence" data-kind={e.kind} data-result={res}>
              <span class="icon" style={{ color: tone[tn].fg }}>
                <Icon icon={RESULT_ICON[res]} size={16} />
              </span>
              <div class="head">
                <Badge tone={tn}>{RESULT_TEXT[res]}</Badge>
                <strong>{evidenceLabel(e.kind)}</strong>
                {/* which run recorded it, once there is more than one (the core counts from 0; the pill says run 1 for the first) */}
                {runs > 1 ? <span mix={muted}>run {e.attempt + 1}</span> : null}
              </div>
              <div class="body">
                <div>{e.summary}</div>
                {link ? (
                  <a href={link} target="_blank" rel="noopener noreferrer" title={link} data-rmx-document>
                    {link}
                  </a>
                ) : null}
              </div>
            </li>
          );
        })}
      </ul>
    );
  };
}

const reviewsCss = css({
  display: 'grid',
  gap: t.s3,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': { display: 'grid', gridTemplateColumns: '24px 1fr', gap: `4px ${t.s3}`, minWidth: '0' },
  '& .head': { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, fontSize: t.textMd },
  '& .text': { gridColumn: '2', minWidth: '0', padding: `${t.s2} ${t.s3}`, borderRadius: t.rMd, background: t.bgSurface, border: `1px solid ${t.border}` },
});

/** The reviewer agents' verdicts (approved or asked for changes) with their feedback, run by run. */
export function ReviewList(handle: Handle<{ reviews: readonly ReviewRecord[]; agents: readonly Agent[] }>) {
  return () => {
    const { reviews, agents } = handle.props;
    if (reviews.length === 0) return <p mix={muted}>No reviewer verdict yet.</p>;
    const runs = new Set(reviews.map((r) => r.attempt)).size;
    return (
      <ul mix={reviewsCss} aria-label="Reviews">
        {reviews.map((r) => {
          const name = agentName(r.reviewer, agents);
          return (
            <li key={r.id} data-testid="review" data-verdict={r.verdict}>
              <Avatar id={r.reviewer} name={name} size={24} kind="agent" />
              <div class="head">
                {/* the verdict in words, never the core's SHIP or REVISE (the decision card and the Inbox say it the same way) */}
                <Badge tone={r.verdict === 'SHIP' ? 'ok' : 'warn'}>{r.verdict === 'SHIP' ? 'Approved' : r.verdict === 'REVISE' ? 'Asked for changes' : r.verdict.toLowerCase().replaceAll('_', ' ')}</Badge>
                <span>
                  by <strong>{name}</strong>
                </span>
                {runs > 1 ? <span mix={muted}>run {r.attempt + 1}</span> : null}
              </div>
              {r.feedback ? (
                <div class="text">
                  <Markdown source={r.feedback} />
                </div>
              ) : null}
            </li>
          );
        })}
      </ul>
    );
  };
}
