// The channel's member list: an avatar stack in the header that opens a popover (Slack's member button).

import { css, on, ref, type Handle } from 'remix/component';
import type { Agent, RoomMember } from '../../api/types.ts';
import { authorName } from '../../lib/format.ts';
import { Avatar } from '../../ui/data.tsx';
import { t, tone, type Tone } from '../../ui/tokens.ts';

const wrapCss = css({
  position: 'relative',
  '& > summary': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '6px',
    height: '30px',
    padding: '0 10px 0 5px',
    border: `1px solid ${t.border}`,
    borderRadius: t.rFull,
    background: t.bgSurface,
    cursor: 'pointer',
    listStyle: 'none',
    color: t.textMuted,
    fontSize: t.textSm,
    fontWeight: '600',
    userSelect: 'none',
  },
  '& > summary::-webkit-details-marker': { display: 'none' },
  '& > summary:hover': { background: t.bgHover, color: t.text },
  '&[open] > summary': { background: t.bgActive, color: t.text },
});
const stackCss = css({ display: 'inline-flex', '& > span': { marginLeft: '-5px', boxShadow: `0 0 0 2px ${t.bgSurface}`, borderRadius: '50%' }, '& > span:first-child': { marginLeft: '0' } });
const popCss = css({
  position: 'absolute',
  right: '0',
  top: 'calc(100% + 6px)',
  zIndex: '20',
  width: '300px',
  maxHeight: '60vh',
  overflowY: 'auto',
  padding: '6px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgOverlay,
  boxShadow: t.shadowMd,
  '& h2': { margin: '4px 8px 6px', color: t.textFaint, fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase' },
  '& ul': { display: 'grid', gap: '1px', margin: '0', padding: '0', listStyle: 'none' },
});
const rowCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: '6px 8px',
  borderRadius: t.rMd,
  '&:hover': { background: t.bgHover },
  '& .who': { display: 'grid', flex: '1', minWidth: '0' },
  '& .who strong': { fontSize: t.textMd, fontWeight: '600', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& .who span': { color: t.textMuted, fontSize: t.textSm },
});
const kindCss = css({ flex: 'none', padding: '1px 6px', borderRadius: t.rSm, fontSize: t.textXs, fontWeight: '600', textTransform: 'uppercase', letterSpacing: '0.04em' });

const KIND_TONE: Record<string, Tone> = { human: 'ok', agent: 'accent', tool: 'info', system: 'muted' };

export function RoomMembers(handle: Handle<{ members: readonly RoomMember[]; agents: readonly Agent[] }>) {
  let node: HTMLDetailsElement | undefined;
  const close = () => {
    if (node?.open) node.open = false;
  };
  return () => {
    const { members, agents } = handle.props;
    if (members.length === 0) return null;
    return (
      <details
        class="members"
        mix={[
          wrapCss,
          ref((n, signal) => {
            node = n as HTMLDetailsElement;
            document.addEventListener(
              'pointerdown',
              (e) => {
                if (node && !node.contains(e.target as Node)) close();
              },
              { signal },
            );
          }),
          on('keydown', (e) => {
            if ((e as KeyboardEvent).key === 'Escape') close();
          }),
        ]}
      >
        <summary aria-label={`Members (${members.length})`} title="Members">
          <span mix={stackCss} aria-hidden="true">
            {members.slice(0, 3).map((mb) => (
              <Avatar key={`${mb.member.kind}:${mb.member.id}`} id={mb.member.id} name={authorName(mb.member, agents)} size={20} kind={mb.member.kind === 'agent' ? 'agent' : mb.member.kind === 'human' ? 'human' : 'system'} />
            ))}
          </span>
          <span aria-hidden="true">{members.length}</span>
        </summary>
        <div mix={popCss}>
          <h2>Members · {members.length}</h2>
          <ul>
            {members.map((mb) => {
              const tn = tone[KIND_TONE[mb.member.kind] ?? 'muted'];
              return (
                <li key={`${mb.member.kind}:${mb.member.id}`} mix={rowCss}>
                  <Avatar id={mb.member.id} name={authorName(mb.member, agents)} size={28} kind={mb.member.kind === 'agent' ? 'agent' : mb.member.kind === 'human' ? 'human' : 'system'} />
                  <div class="who">
                    <strong>{authorName(mb.member, agents)}</strong>
                    <span>
                      {mb.role ? `${mb.role} · ` : ''}
                      {mb.state}
                    </span>
                  </div>
                  <span mix={kindCss} style={{ color: tn.fg, background: tn.bg }}>
                    {mb.member.kind}
                  </span>
                </li>
              );
            })}
          </ul>
        </div>
      </details>
    );
  };
}
