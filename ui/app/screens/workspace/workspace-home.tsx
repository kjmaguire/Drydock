// Home (/home, and the first run; decision P142): the start screen. "What do you want to build?", one box with its chips
// (start-box.tsx), the "Or:" row, then "Pick up where you left off": the most recently active projects as cards, each with what
// runs, whether it needs you and one line of what is happening. Opening a card selects that project and opens it.

import { css, navigate, on, type Handle } from 'remix/component';
import { ArrowRight } from 'lucide';
import type { Project } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { RUNNING_STATUSES } from '../../state/workspace.ts';
import { services, watch } from '../../state/services.tsx';
import { hueFor } from '../../ui/data.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { relTime } from '../chats/chats.ts';
import { StartBox } from './start-box.tsx';
import { pickUp, type PickUp } from './workspace.ts';

const wrapCss = css({
  display: 'grid',
  gap: t.s4,
  width: '100%',
  maxWidth: '840px',
  margin: `clamp(16px, 9vh, 96px) auto ${t.s8}`,
  '& h1': { margin: '0', fontSize: t.text2xl, fontWeight: '600', letterSpacing: '-0.01em' },
  '& .lead': { margin: '-8px 0 0', color: t.textMuted, fontSize: t.textMd },
});
const pickCss = css({
  display: 'grid',
  gap: t.s3,
  marginTop: t.s6,
  '& .head': { display: 'flex', alignItems: 'baseline', gap: t.s3 },
  '& h2': { margin: '0', fontSize: t.textMd, fontWeight: '600' },
  '& .all': {
    marginLeft: 'auto',
    display: 'inline-flex',
    alignItems: 'center',
    gap: '4px',
    padding: '0',
    minHeight: '0',
    border: '0',
    background: 'transparent',
    color: t.textMuted,
    fontSize: t.textSm,
  },
  '& .all:hover': { color: t.text },
  '& ul': { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: t.s3, margin: '0', padding: '0', listStyle: 'none' },
});
const cardCss = css({
  display: 'grid',
  alignContent: 'start',
  gap: '6px',
  height: '100%',
  padding: `${t.s3} ${t.s4}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  color: t.text,
  textDecoration: 'none',
  '&:hover': { borderColor: t.borderStrong, textDecoration: 'none' },
  '& .top': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0' },
  '& .dot': { width: '9px', height: '9px', borderRadius: '2px', flex: 'none' },
  '& strong': { flex: '1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: '600' },
  '& .pill': { flex: 'none', padding: '1px 8px', borderRadius: t.rFull, fontSize: t.textXs, color: t.textMuted, whiteSpace: 'nowrap' },
  '& .pill[data-tone="run"]': { background: t.accentSoft, color: t.accentText },
  '& .pill[data-tone="warn"]': { background: t.warningSoft, color: t.warning },
  '& .line': { color: t.textMuted, fontSize: t.textSm, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
});

/** One project to pick up: its colour, name, a status pill and one line. Opening it selects the project first. */
function PickCard(handle: Handle<{ item: PickUp; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  const open = (e: MouseEvent) => {
    e.preventDefault();
    s.workspace.selectProject(handle.props.item.project.id);
    if (handle.props.onNavigate) handle.props.onNavigate(routes.home.href());
    else void navigate(routes.home.href());
  };
  return () => {
    const { project: p, running, needs, last, line } = handle.props.item;
    const pill = needs > 0 ? { tone: 'warn', text: 'needs you' } : running > 0 ? { tone: 'run', text: `${running} running` } : { tone: '', text: relTime(last) };
    return (
      <li>
        <a href={routes.home.href()} mix={[cardCss, on<HTMLAnchorElement, 'click'>('click', open)]} data-testid="home-project" data-project={p.id} aria-label={`Open ${p.name}`}>
          <span class="top">
            <span class="dot" aria-hidden="true" style={{ background: `hsl(${hueFor(p.id)} 62% 62%)` }} />
            <strong>{p.name}</strong>
            {pill.text ? (
              <span class="pill" data-tone={pill.tone || undefined} data-testid="home-project-state">
                {pill.text}
              </span>
            ) : null}
          </span>
          <span class="line" title={line} data-testid="home-project-line">
            {line}
          </span>
        </a>
      </li>
    );
  };
}

export function WorkspaceHomeScreen(handle: Handle<{ onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  // a note the previous page left (a project was deleted, what a start could not do)
  const flash = s.ui.takeFlash();
  let all = false;

  return () => {
    const ws = s.workspace;
    const projects: Project[] = ws.projectList;
    const items = pickUp(projects, ws.summaries.data?.summaries ?? [], ws.needs.data ?? [], RUNNING_STATUSES);
    const shown = all ? items : items.slice(0, 3);
    return (
      <Page
        title="What do you want to build?"
        testId="workspace-home"
        head={(id) => (
          <div mix={wrapCss}>
            <h1 id={id}>What do you want to build?</h1>
            <p class="lead">Describe it; change the place or the crew below.</p>
            {flash ? (
              <Notice tone={flash.tone} role="status" testId="home-flash">
                {flash.text}
              </Notice>
            ) : null}
            <StartBox onNavigate={handle.props.onNavigate} />
            <ErrorNote error={ws.projects.error} what="Projects" />
            {items.length ? (
              <section mix={pickCss} aria-labelledby={`${id}-pick`} data-testid="home-projects">
                <div class="head">
                  <h2 id={`${id}-pick`}>Pick up where you left off</h2>
                  {items.length > 3 ? (
                    <button type="button" class="all" data-testid="home-all-projects" aria-expanded={all ? 'true' : 'false'} mix={on('click', () => ((all = !all), void handle.update()))}>
                      {all ? 'Fewer projects' : 'All projects'} <Icon icon={ArrowRight} size={12} />
                    </button>
                  ) : null}
                </div>
                <ul aria-label="Projects">
                  {shown.map((it) => (
                    <PickCard key={it.project.id} item={it} onNavigate={handle.props.onNavigate} />
                  ))}
                </ul>
              </section>
            ) : null}
          </div>
        )}
      />
    );
  };
}
