// What a hub shows beside the sidebar (graphite, decision P97; first drawn for Crew HQ, P64): a row of pills for a hub's sub-pages
// (Overview, Tasks, Knowledge, the Agents library, a Settings section of more than one screen), the Settings rail (decision
// P140: "Settings", a search box and the sections of shell/settings-sections.ts) and the Rooms rail (the project's channels,
// task rooms and direct messages). Every page the old sidebar linked is reached from one of these, with the same test id. A
// project's own settings (Project settings, its Integrations) are pills of its Overview, set apart at the end of the row: they
// are the project's, so they are found inside the project, not in Settings.

import { css, on, type Handle, type RemixNode } from 'remix/component';
import { BellOff, Search, Settings2 } from 'lucide';
import type { Room } from '../api/types.ts';
import { roomLabel } from '../lib/format.ts';
import { hubOf, type Route } from '../lib/route.ts';
import { routes } from '../routes.ts';
import { services, watch } from '../state/services.tsx';
import { Avatar, presenceColor } from '../ui/data.tsx';
import { Icon, roomIcon } from '../ui/icon.tsx';
import { t } from '../ui/tokens.ts';
import { tightList } from '../ui/layout.tsx';
import { searchSettings, sectionOf } from './settings-sections.ts';

const subCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: '4px',
  flex: 'none',
  padding: `${t.s2} ${t.s6}`,
  borderBottom: `1px solid ${t.border}`,
  background: t.bgApp,
  overflowX: 'auto',
});
const pillCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  height: '28px',
  padding: `0 ${t.s3}`,
  borderRadius: t.rMd,
  color: t.textMuted,
  fontSize: t.textSm,
  fontWeight: '500',
  textDecoration: 'none',
  whiteSpace: 'nowrap',
  border: '1px solid transparent',
  '&:hover': { background: t.bgHover, color: t.text, textDecoration: 'none' },
  '&[aria-current="page"]': { background: t.bgActive, color: t.text, borderColor: t.border },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
});
// the project's own settings sit at the end of the Overview's row, after a gap and a hairline
const spacerCss = css({ flex: '1', minWidth: t.s3 });
const dividerCss = css({ flex: 'none', width: '1px', height: '18px', margin: `0 ${t.s2}`, background: t.border });

interface Pill {
  label: string;
  href: string;
  testId: string;
  /** The route names this pill is current on. */
  on: Route['name'][];
  /** Set apart at the end of the row: the pages that configure the hub's subject rather than show it. */
  trailing?: boolean;
  icon?: typeof Settings2;
}

// the hub's name, before its pills, where the hub is a page of its own rather than a tab of a project (Agents)
const TITLE_CSS = css({ flex: 'none', margin: `0 ${t.s3} 0 0`, fontSize: t.textLg, fontWeight: '600', color: t.text });

const PILLS: Partial<Record<ReturnType<typeof hubOf>, Pill[]>> = {
  // one Agents library (decision P140): the person's own agents (the templates), the prebuilt starters, the skills; every
  // project's crew is set apart at the end
  library: [
    { label: 'My agents', href: routes.library.href(), testId: 'nav-library-mine', on: ['library'] },
    { label: 'Starters', href: routes.libraryStarters.href(), testId: 'nav-library-starters', on: ['library-starters'] },
    { label: 'Skills', href: routes.librarySkills.href(), testId: 'nav-library-skills', on: ['library-skills'] },
    { label: 'All crews', href: routes.crew.href(), testId: 'nav-crew', on: ['crew'], trailing: true },
  ],
  overview: [
    { label: 'Overview', href: routes.home.href(), testId: 'nav-overview-home', on: ['home'] },
    { label: 'Activity', href: routes.activity.href(), testId: 'nav-activity', on: ['activity'] },
    { label: 'Budgets', href: routes.budgets.href(), testId: 'nav-budgets', on: ['budgets'] },
    { label: 'Evals report', href: routes.evals.href(), testId: 'nav-evals', on: ['evals'] },
    { label: 'Security', href: routes.security.href(), testId: 'nav-security', on: ['security'] },
    // the project's own settings: moved here from the Settings rail, so they are found inside the project
    { label: 'Project settings', href: routes.project.href(), testId: 'nav-project', on: ['project'], trailing: true, icon: Settings2 },
    { label: 'Integrations', href: routes.projectIntegrations.href(), testId: 'nav-project-integrations', on: ['project-integrations'], trailing: true },
  ],
  tasks: [
    { label: 'Board', href: routes.tasks.href(), testId: 'nav-tasks-board', on: ['tasks', 'task', 'task-new', 'run'] },
    { label: 'Plan', href: routes.plan.href(), testId: 'nav-plan', on: ['plan'] },
    { label: 'Pull requests', href: routes.pulls.href(), testId: 'nav-pulls', on: ['pulls'] },
    { label: 'Milestones', href: routes.milestones.href(), testId: 'nav-milestones', on: ['milestones'] },
  ],
  knowledge: [
    { label: 'Overview', href: routes.knowledge.href(), testId: 'nav-knowledge-home', on: ['knowledge'] },
    { label: 'Requirements', href: routes.requirements.href(), testId: 'nav-requirements', on: ['requirements'] },
    { label: 'Expert packs', href: routes.packs.href(), testId: 'nav-packs', on: ['packs'] },
    { label: 'Skills', href: routes.skills.href(), testId: 'nav-skills', on: ['skills'] },
    { label: 'Memory', href: routes.memory.href(), testId: 'nav-memory', on: ['memory'] },
    { label: 'Repo map', href: routes.repoMap.href(), testId: 'nav-repo-map', on: ['repo-map'] },
  ],
};

const HUB_TITLES: Partial<Record<ReturnType<typeof hubOf>, string>> = { library: 'Agents' };

/** A Settings section's screens as pills, when it has more than one (Backups & data, Help & about). */
function settingsPills(route: Route): Pill[] | undefined {
  const sec = sectionOf(route);
  if (!sec || sec.pages.length < 2) return undefined;
  return sec.pages.map((p) => ({ label: p.label, href: p.href, testId: p.testId, on: [...p.on] }));
}

/**
 * The pills of the route's hub, or nothing when the hub has one page. The trailing pills come after a gap and a hairline. In
 * Settings, the pills are the screens of the section open, when it has more than one.
 */
export function SubNav(handle: Handle<{ route: Route }>) {
  return () => {
    const { route } = handle.props;
    const hub = hubOf(route);
    const pills = hub === 'settings' ? settingsPills(route) : PILLS[hub];
    if (!pills) return null;
    const title = HUB_TITLES[hub];
    const first = pills.findIndex((p) => p.trailing);
    const pill = (p: Pill) => (
      <a key={p.testId} href={p.href} mix={pillCss} data-testid={p.testId} aria-current={p.on.includes(route.name) ? 'page' : undefined}>
        {p.icon ? <Icon icon={p.icon} size={14} /> : null}
        {p.label}
      </a>
    );
    return (
      <nav aria-label="Section" mix={subCss}>
        {title ? <span mix={TITLE_CSS}>{title}</span> : null}
        {(first < 0 ? pills : pills.slice(0, first)).map(pill)}
        {first >= 0 ? (
          <>
            <span mix={spacerCss} aria-hidden="true" />
            <span mix={dividerCss} aria-hidden="true" />
            {pills.slice(first).map(pill)}
          </>
        ) : null}
      </nav>
    );
  };
}

// --- rails -------------------------------------------------------------------------------------------------------------

const railCss = css({
  display: 'flex',
  flexDirection: 'column',
  gap: '2px',
  flex: 'none',
  width: 'var(--sidebar-width)',
  minHeight: '0',
  overflowY: 'auto',
  padding: `${t.s4} ${t.s3}`,
  borderRight: `1px solid ${t.border}`,
  background: t.bgSidebar,
  // a group's heading is the first thing in its own box, so the space between two groups is the box's
  '& > div + div': { marginTop: t.s4 },
  // a narrow window shows the rail only as a drawer over the page, opened from the shell's button (ui.ts NARROW_RAILS)
  '@media (max-width: 899px)': { position: 'absolute', top: '0', left: '0', bottom: '0', zIndex: '30', maxWidth: 'calc(100% - 48px)', boxShadow: t.shadowLg },
});
// the Settings rail's labels are short: it is narrower than the Rooms rail, so the page beside it keeps its room
const settingsRailCss = css({ width: '232px', gap: '0' });
const groupHead = css({ margin: `${t.s5} ${t.s2} ${t.s1}`, color: t.textFaint, fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.06em', textTransform: 'uppercase', '&:first-child': { marginTop: '0' } });
const listCss = tightList;
const itemCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  height: '34px',
  padding: `0 ${t.s2}`,
  borderRadius: t.rMd,
  color: t.textMuted,
  fontSize: t.textMd,
  fontWeight: '500',
  textDecoration: 'none',
  whiteSpace: 'nowrap',
  minWidth: '0',
  // width 0 and grown to fit: a long name adds nothing to the row's smallest width, so the row stays as wide as its column (a card's
  // or the rail's) and the ellipsis shows, instead of the row running past the card (the UI audit of 2026-10-26)
  '& > .label': { flex: '1 1 0', width: '0', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& > .room-icon': { display: 'inline-grid', placeItems: 'center', width: '26px', height: '26px', flex: 'none', borderRadius: '8px', background: t.bgActive, color: t.textMuted },
  '&:hover': { background: t.bgHover, color: t.text, textDecoration: 'none' },
  '&[aria-current="page"]': { background: t.bgSurface, color: t.text, fontWeight: '600', boxShadow: t.shadowSm },
  '&[data-unread="true"]': { color: t.text, fontWeight: '600' },
  '&:focus-visible': { boxShadow: `inset ${t.focus}`, outline: 'none' },
});
const mutedCss = css({ flex: 'none', color: t.textFaint, lineHeight: '0' });
// the unread count is a quiet number, read once ("general, 3 unread")
const countCss = css({ minWidth: '18px', height: '18px', padding: '0 5px', borderRadius: t.rFull, background: t.accent, color: t.onAccent, fontSize: t.textXs, fontWeight: '700', lineHeight: '18px', textAlign: 'center', fontVariantNumeric: 'tabular-nums' });

function Group(handle: Handle<{ title: string; children?: RemixNode }>) {
  return () => (
    <div>
      <h2 mix={groupHead}>{handle.props.title}</h2>
      <ul mix={listCss}>{handle.props.children}</ul>
    </div>
  );
}

const searchBox = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  height: '32px',
  margin: `0 0 ${t.s3}`,
  padding: `0 ${t.s2}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  color: t.textFaint,
  '&:focus-within': { borderColor: t.accent, boxShadow: t.focus },
  '& > input': { flex: '1', minWidth: '0', height: '30px', padding: '0', border: '0', background: 'transparent', color: t.text, fontSize: t.textMd, outline: 'none', boxShadow: 'none' },
});
const railTitle = css({ margin: `0 ${t.s2} ${t.s3}`, fontSize: t.textLg, fontWeight: '600' });
const hitRows = css({ listStyle: 'none', margin: `0 0 ${t.s1}`, padding: '0' });
const hitRow = css({
  display: 'block',
  padding: `3px ${t.s2} 3px ${t.s5}`,
  borderRadius: t.rMd,
  color: t.textFaint,
  fontSize: t.textSm,
  textDecoration: 'none',
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  '&:hover': { background: t.bgHover, color: t.text, textDecoration: 'none' },
});
const noHit = css({ margin: `${t.s2}`, color: t.textMuted, fontSize: t.textSm });

/**
 * The Settings frame's left list (decision P140): "Settings", a search box that keeps the sections whose title or rows match
 * (and lists the rows it found under each), then the sections (shell/settings-sections.ts). Every section keeps its old test
 * id (nav-side-agents is Agent CLIs, nav-settings-about is General). Nothing of one project is here: Project settings and a
 * project's Integrations are pills of the project's Overview.
 */
export function SettingsRail(handle: Handle<{ route: Route }>) {
  let q = '';
  return () => {
    const { route } = handle.props;
    const current = sectionOf(route);
    const hits = searchSettings(q);
    return (
      <nav aria-label="Settings" mix={[railCss, settingsRailCss]}>
        <h2 mix={railTitle}>Settings</h2>
        <label mix={searchBox}>
          <Icon icon={Search} size={14} />
          <input
            type="search"
            value={q}
            placeholder="Search settings"
            aria-label="Search settings"
            data-testid="settings-search"
            mix={[
              on('input', (e) => {
                q = (e.currentTarget as HTMLInputElement).value;
                void handle.update();
              }),
              on('keydown', (e) => {
                if (e.key === 'Escape' && q) {
                  e.stopPropagation();
                  q = '';
                  void handle.update();
                }
              }),
            ]}
          />
        </label>
        {hits.length === 0 ? (
          <p mix={noHit} data-testid="settings-no-match">
            No setting matches "{q}".
          </p>
        ) : null}
        <ul mix={listCss} aria-label="Sections">
          {hits.map(({ section, rows }) => (
            <li key={section.id} data-section={section.id}>
              <a href={section.href} mix={itemCss} data-testid={section.testId} aria-current={current?.id === section.id ? 'page' : undefined}>
                <span class="label">{section.title}</span>
              </a>
              {rows.length ? (
                <ul mix={hitRows} aria-label={`${section.title}: what matches`}>
                  {rows.map((row) => (
                    <li key={row}>
                      <a href={section.pages.find((p) => p.label === row)?.href ?? section.href} mix={hitRow} data-testid="settings-hit">
                        {row}
                      </a>
                    </li>
                  ))}
                </ul>
              ) : null}
            </li>
          ))}
        </ul>
      </nav>
    );
  };
}

/**
 * A room's link: an icon (or the agent's dot for a direct message), the name, and a quiet unread count. Rooms are named
 * without "#", so the link's accessible name starts with the room name. Shared by the Rooms rail and the Overview's rooms card.
 */
export function RoomLink(handle: Handle<{ room: Room; current: boolean }>) {
  const s = services(handle);
  watch(handle, s.project);
  return () => {
    const { room: r, current } = handle.props;
    const p = s.project;
    const agents = p.agentList;
    const u = (p.unread.data ?? []).find((x) => x.roomId === r.id);
    const n = u?.unread ?? 0;
    // a muted room (decision P130) counts only the messages that name the person, and says it is muted
    const muted = !!u?.muted;
    // a direct message shows the agent, a task room its writer (both as dots); a channel its icon
    const agentId = r.kind === 'dm' ? r.agentId || r.name.slice(r.name.lastIndexOf('+') + 1) : r.kind === 'task' ? (p.taskList.find((x) => x.id === r.taskId)?.assignee ?? '') : '';
    const agent = agentId ? agents.find((a) => a.id === agentId) : undefined;
    const label = roomLabel(r, agents);
    return (
      <li>
        <a
          href={routes.room.href({ id: r.id })}
          mix={itemCss}
          data-testid="room-link"
          aria-current={current ? 'page' : undefined}
          data-unread={n > 0 ? 'true' : undefined}
          data-muted={muted ? 'true' : undefined}
        >
          {r.kind === 'dm' || (r.kind === 'task' && agent) ? (
            <Avatar id={agentId} name={agent?.name ?? label} size={24} kind="agent" status={presenceColor(p.presence.data?.find((x) => x.agentId === agentId))} />
          ) : (
            <span class="room-icon">
              <Icon icon={roomIcon(r.kind)} size={14} />
            </span>
          )}
          {/* "muted" is read with the name; the icon is only drawn */}
          <span class="label">
            {label}
            {muted ? <span class="sr-only">, muted</span> : null}
          </span>
          {muted ? (
            <span mix={mutedCss} title="Muted: only messages that name you count" data-testid="room-muted" aria-hidden="true">
              <Icon icon={BellOff} size={13} />
            </span>
          ) : null}
          {n > 0 ? (
            <span mix={countCss} title={`${n} unread`} data-testid="unread-count">
              <span aria-hidden="true">{n}</span>
              <span class="sr-only">, {n} unread</span>
            </span>
          ) : null}
        </a>
      </li>
    );
  };
}

/** The Rooms rail: team rooms, task rooms and direct messages of the open project. */
export function RoomsRail(handle: Handle<{ route: Route }>) {
  const s = services(handle);
  watch(handle, s.project);
  return () => {
    const { route } = handle.props;
    const rooms = s.project.roomList.filter((r) => !r.archived);
    const current = (r: Room) => route.name === 'room' && route.id === r.id;
    const of = (kind: Room['kind']) => rooms.filter((r) => r.kind === kind).map((r) => <RoomLink key={r.id} room={r} current={current(r)} />);
    return (
      <nav aria-label="Rooms" mix={railCss} data-rooms="">
        <Group title="Team rooms">{of('channel')}</Group>
        {rooms.some((r) => r.kind === 'task') ? <Group title="Task rooms">{of('task')}</Group> : null}
        {rooms.some((r) => r.kind === 'dm') ? <Group title="Direct messages">{of('dm')}</Group> : null}
      </nav>
    );
  };
}
