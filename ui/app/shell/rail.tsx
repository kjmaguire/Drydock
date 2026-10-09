// The project rail (Discord's server list): one rounded tile per project, the selected one marked by a pill, then "add a
// project", and the workspace shortcuts at the bottom.

import { css, on, type Handle } from 'remix/component';
import { Anchor, Plus, Settings } from 'lucide';
import { routes } from '../routes.ts';
import { services, watch } from '../state/services.tsx';
import { Avatar } from '../ui/data.tsx';
import { Icon, type IconNode } from '../ui/icon.tsx';
import { t } from '../ui/tokens.ts';
import type { Route } from '../lib/route.ts';

const railCss = css({
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  gap: t.s2,
  width: 'var(--rail-width)',
  padding: `${t.s3} 0`,
  background: t.bgRail,
  overflowY: 'auto',
  scrollbarWidth: 'none',
});

const tileWrap = css({
  position: 'relative',
  display: 'flex',
  justifyContent: 'center',
  width: '100%',
  '&::before': {
    content: '""',
    position: 'absolute',
    left: '0',
    top: '50%',
    width: '4px',
    height: '0',
    borderRadius: '0 4px 4px 0',
    background: t.text,
    transform: 'translateY(-50%)',
    transition: `height ${t.med} ${t.ease}`,
  },
  '&:hover::before': { height: '18px' },
  '&[data-current="true"]::before': { height: '36px' },
});

const tileCss = css({
  display: 'grid',
  placeItems: 'center',
  width: '40px',
  height: '40px',
  padding: '0',
  border: '0',
  borderRadius: '12px',
  background: 'transparent',
  color: t.textMuted,
  overflow: 'hidden',
  transition: `border-radius ${t.med} ${t.ease}, background-color ${t.fast} ${t.ease}, color ${t.fast} ${t.ease}`,
  '&:hover': { borderRadius: '14px', background: 'transparent' },
  '& > span': { transition: `border-radius ${t.med} ${t.ease}` },
});

const iconTile = css({
  background: t.bgSurface,
  borderRadius: '50%',
  '&:hover, &[aria-current="page"]': { background: t.accent, color: t.onAccent, borderRadius: '14px' },
});

const addTile = css({
  background: t.bgSurface,
  color: t.success,
  borderRadius: '50%',
  '&:hover, &[aria-current="page"]': { background: t.success, color: t.onAccent, borderRadius: '14px' },
});

const brandCss = css({
  display: 'grid',
  placeItems: 'center',
  width: '40px',
  height: '40px',
  borderRadius: '12px',
  background: `linear-gradient(135deg, ${t.accent}, #9b6cf0)`,
  color: '#fff',
  boxShadow: t.shadowSm,
});

const sep = css({ width: '28px', height: '2px', margin: `${t.s1} 0`, borderRadius: '1px', background: t.border, flex: 'none' });
const grow = css({ flex: '1' });

function RailLink(handle: Handle<{ href: string; label: string; icon: IconNode; current: boolean; testId?: string; tone?: 'add' }>) {
  return () => {
    const { href, label, icon, current, testId, tone } = handle.props;
    return (
      <div mix={tileWrap} data-current={String(current)}>
        <a href={href} aria-label={label} title={label} data-testid={testId} aria-current={current ? 'page' : undefined} mix={[tileCss, tone === 'add' ? addTile : iconTile]}>
          <Icon icon={icon} size={20} />
        </a>
      </div>
    );
  };
}

export function Rail(handle: Handle<{ route: Route }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  // a project is chosen like a radio button: arrow keys move between the tiles and select
  const onKey = (e: KeyboardEvent) => {
    if (!['ArrowDown', 'ArrowUp', 'ArrowRight', 'ArrowLeft'].includes(e.key)) return;
    const list = s.workspace.projectList;
    if (list.length === 0) return;
    e.preventDefault();
    const i = Math.max(0, list.findIndex((p) => p.id === s.workspace.projectId));
    const next = list[(i + (e.key === 'ArrowDown' || e.key === 'ArrowRight' ? 1 : list.length - 1)) % list.length]!;
    s.workspace.selectProject(next.id);
    handle.queueTask(() => (document.querySelector(`[data-testid="project-tile"][data-project="${CSS.escape(next.id)}"]`) as HTMLElement | null)?.focus());
  };
  return () => {
    const { route } = handle.props;
    const current = s.workspace.project;
    return (
      <nav mix={railCss} aria-label="Projects">
        <span mix={brandCss} title="Drydock">
          <Icon icon={Anchor} size={20} label="Drydock" />
        </span>
        <span mix={sep} aria-hidden="true" />
        <div role="radiogroup" aria-label="Projects" style={{ display: 'contents' }} mix={on<HTMLDivElement, 'keydown'>('keydown', onKey)}>
          {s.workspace.projectList.map((p) => {
            const sel = p.id === current?.id;
            return (
              <div key={p.id} mix={tileWrap} data-current={String(sel)}>
                <button
                  type="button"
                  role="radio"
                  mix={[tileCss, on('click', () => s.workspace.selectProject(p.id))]}
                  aria-label={p.name}
                  aria-checked={sel}
                  tabIndex={sel ? 0 : -1}
                  title={p.name}
                  data-testid="project-tile"
                  data-project={p.id}
                >
                  <Avatar id={p.id} name={p.name} size={40} square />
                </button>
              </div>
            );
          })}
        </div>
        <RailLink href={routes.projectNew.href()} label="+ New project" icon={Plus} current={route.name === 'project-new'} tone="add" />
        <span mix={grow} />
        <RailLink href={routes.settings.href()} label="App settings" icon={Settings} current={route.name === 'settings'} />
      </nav>
    );
  };
}
