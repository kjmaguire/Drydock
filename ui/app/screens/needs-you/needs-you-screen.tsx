// Needs You: everything waiting on the human (approvals, reviews, questions, failed checks, watchdog pauses), most urgent
// first, each a link to where it is decided. Nothing can be approved from here: the decision happens on the item's page.

import { css, type Handle } from 'remix/component';
import { BadgeCheck, CircleHelp, CirclePause, Eye, Inbox, OctagonX } from 'lucide';
import { agentName, formatDateTime } from '../../lib/format.ts';
import { services, watch } from '../../state/services.tsx';
import { buttonStyle } from '../../ui/button.tsx';
import { Avatar, Badge } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon, type IconNode } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { KIND_LABEL, needsHint, needsTarget } from './needs-you.ts';

const KIND_ICON: Record<string, IconNode> = {
  approval: BadgeCheck,
  review: Eye,
  question: CircleHelp,
  failed_check: OctagonX,
  watchdog_pause: CirclePause,
};

// one card per item (Crew HQ): the whole card is the link to where the item is decided
const listCss = css({ display: 'grid', gap: t.s3, margin: '0', padding: '0', listStyle: 'none' });

const itemCss = css({
  '& > a': {
    display: 'grid',
    gridTemplateColumns: '40px 1fr auto',
    gap: `3px ${t.s3}`,
    alignItems: 'start',
    padding: '18px',
    border: `1px solid ${t.mentionBorder}`,
    borderRadius: t.rXl,
    background: t.mentionBg,
    color: t.text,
    textDecoration: 'none',
    transition: `border-color ${t.fast} ${t.ease}, transform ${t.fast} ${t.ease}`,
  },
  '&[data-urgent="true"] > a': { borderColor: t.danger, background: t.dangerSoft },
  '& > a:hover': { textDecoration: 'none', transform: 'translateY(-1px)' },
  '& .title': { fontWeight: '600', fontSize: t.textLg, letterSpacing: '-0.01em', minWidth: '0', overflowWrap: 'anywhere' },
  '& .meta': { gridColumn: '2', display: 'flex', alignItems: 'center', gap: t.s2, color: t.textMuted, fontSize: t.textSm },
  '& .detail, & .hint': { gridColumn: '2', color: t.textMuted, fontSize: t.textSm, lineHeight: '1.5' },
  '& .hint': { color: t.text, marginTop: '4px' },
  '& .open': { gridColumn: '2', marginTop: t.s2, justifySelf: 'start' },
});

const kindIcon = css({ display: 'grid', placeItems: 'center', width: '40px', height: '40px', borderRadius: '50%' });

export function NeedsYouScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.project);
  return () => {
    const q = s.project.needs;
    const items = q.data ?? [];
    const agents = s.project.agentList;
    return (
      <Page title="Needs you" icon={Inbox} subtitle={q.data ? `${items.length} waiting · each opens where you decide it` : undefined}>
        <ErrorNote error={q.error} what="Could not load the inbox" />
        {q.loading && !q.data ? <Loading /> : null}
        {q.data && items.length === 0 ? (
          <Empty title="Nothing is waiting for you" icon={Inbox}>
            <p>Approvals, reviews, questions, failed checks and watchdog pauses appear here as they happen.</p>
          </Empty>
        ) : null}
        {items.length > 0 ? (
          <ul mix={listCss} aria-label="Items that need you">
            {items.map((it) => {
              const urgent = it.severity <= 1;
              const hint = needsHint(it);
              return (
                <li key={it.key} mix={itemCss} data-testid="needs-item" data-kind={it.kind} data-urgent={urgent ? 'true' : undefined}>
                  <a href={needsTarget(it)}>
                    {it.agentId ? (
                      <Avatar id={it.agentId} name={agentName(it.agentId, agents)} size={40} kind="agent" status={urgent ? 'var(--danger)' : 'var(--warning)'} />
                    ) : (
                      <span mix={kindIcon} style={{ background: urgent ? 'var(--danger-soft)' : 'var(--warning-soft)', color: urgent ? 'var(--danger)' : 'var(--warning)' }}>
                        <Icon icon={KIND_ICON[it.kind] ?? Inbox} size={18} />
                      </span>
                    )}
                    <span class="title">{it.title}</span>
                    <Badge tone={urgent ? 'err' : 'warn'}>{KIND_LABEL[it.kind] ?? it.kind}</Badge>
                    <span class="meta">
                      {it.agentId ? (
                        <>
                          <Avatar id={it.agentId} name={agentName(it.agentId, agents)} size={16} kind="agent" />
                          <span>{agentName(it.agentId, agents)}</span>
                          <span aria-hidden="true">·</span>
                        </>
                      ) : null}
                      <span>{formatDateTime(it.createdAt)}</span>
                    </span>
                    {it.detail ? <span class="detail">{it.detail}</span> : null}
                    {hint ? (
                      <span class="hint" data-testid="needs-hint">
                        {hint}
                      </span>
                    ) : null}
                    <span class="open" mix={buttonStyle('primary', 'sm')}>
                      Open and decide
                    </span>
                  </a>
                </li>
              );
            })}
          </ul>
        ) : null}
      </Page>
    );
  };
}
