// Crew (graphite, decision P97): the agents as a team. The header bar says how many agents there are, how many work and how
// many wait on the person, with Templates (the prebuilt gallery and the saved templates) and New agent (N). Then "Today": a
// timeline from 08:00 (or the first run's hour) to now with one lane per agent, its runs and its waits on the person
// (agent.timeline, re-read on agent.*, run.* and approval.* events, "now" moving every minute). Then a card per agent: who it is,
// what it runs on, its state, what it is doing or did, how far its task is, its skills and rooms. A card opens the agent's page;
// one that waits on the person opens the Inbox. The same page shows one project's crew (/team) or every project's (/crew,
// the cards grouped by project).

import { css, on, type Handle } from 'remix/component';
import { Users } from 'lucide';
import type { Skill } from '../../api/extTypes.ts';
import type { AgentLane, CrewTimeline } from '../../api/inboxClient.ts';
import type { AdapterInfo, Agent, Connection, PresenceState, Project, TaskSummary } from '../../api/types.ts';
import type { ProjectRunMode } from '../../api/wslClient.ts';
import { ownsKeys } from '../../lib/keys.ts';
import { skipsApprovals } from '../../lib/permission.ts';
import { presenceFor } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar, hueFor } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Icon, roomIcon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { Select } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { SkipsBadge } from '../shared/skip-approvals.tsx';
import { AddPrebuilt } from './add-prebuilt.tsx';
import { AgentForm, FromTemplate } from './agent-forms.tsx';
import { TodayTimeline, type TimelineLane } from './crew-timeline.tsx';
import {
  CARD_WORDS,
  MINUTE,
  agentLine,
  cardState,
  currentSummary,
  lastFinished,
  localIds,
  localMidnight,
  nextQueued,
  openWait,
  progressOf,
  roomsOf,
  runtimeName,
  timelineWindow,
  type CardState,
  type CrewRoom,
  type LinePart,
} from './crew.ts';
import { crewFigures, runsWhereTitle, sortAdapters } from './team.ts';

/** A task's identifier: "GOC-41" with the project's key, "#41" without one (an older core). */
const keyText = (seq: number, projectKey?: string): string => (projectKey ? `${projectKey}-${seq}` : `#${seq}`);

/** The colour square of a project, as the sidebar draws it. */
export const projectColor = (id: string): string => `hsl(${hueFor(id)} 62% 62%)`;

/** A read whose failure leaves the page as it is without it (an older core, a machine without models on it). */
const orElse = async <T,>(read: () => Promise<T>, fallback: T): Promise<T> => {
  try {
    return await read();
  } catch {
    return fallback;
  }
};

/** Typing in a field: single-letter shortcuts stay out of it. */
const typing = (target: EventTarget | null): boolean => {
  const el = target as HTMLElement | null;
  return !!el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName ?? ''));
};

const kbdOnAccent = css({ background: 'transparent', borderColor: `color-mix(in srgb, ${t.onAccent} 35%, transparent)`, color: t.onAccent });

const cardsCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))',
  gap: '14px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
});

const agentsCss = css({ display: 'grid', gap: t.s5, padding: `18px ${t.s6} 48px` });

const groupHead = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  margin: `0 0 ${t.s3}`,
  fontSize: t.textMd,
  fontWeight: '600',
  '& .sq': { width: '9px', height: '9px', borderRadius: '3px' },
  '& .count': { color: t.textFaint, fontWeight: '400', fontSize: t.textSm },
});

// one agent: the card is one link (its name stretched over it), so nothing inside it is a control of its own
const cardCss = css({
  position: 'relative',
  display: 'flex',
  flexDirection: 'column',
  gap: t.s3,
  height: '100%',
  boxSizing: 'border-box',
  padding: '14px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  color: t.text,
  minWidth: '0',
  transition: `border-color ${t.fast} ${t.ease}`,
  '&:hover': { borderColor: t.borderStrong },
  '&:has(a.stretch:focus-visible)': { boxShadow: t.focus },
  '&[data-state="waiting-on-you"]': { borderColor: `color-mix(in srgb, ${t.warning} 40%, ${t.border})`, background: `color-mix(in srgb, ${t.warning} 6%, ${t.bgSurface})` },
  '& a.stretch': { color: 'inherit', textDecoration: 'none', outline: 'none' },
  '& a.stretch::after': { content: '""', position: 'absolute', inset: '0', borderRadius: t.rLg },
  '& .top': { display: 'flex', alignItems: 'center', gap: '10px', minWidth: '0' },
  '& .who': { display: 'flex', flexDirection: 'column', gap: '2px', minWidth: '0' },
  // the name is a 28 px target (the stretched link's own box is what a pointer is measured by), drawn in the line's height
  '& .name': { display: 'block', minHeight: '28px', margin: '-5px 0', lineHeight: '28px', fontWeight: '600', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', maxWidth: '100%' },
  // a waiting card's name opens the agent's page above the card's own link (to the Inbox)
  '& a.above': { position: 'relative', zIndex: '1', color: t.text, textDecoration: 'none', alignSelf: 'flex-start' },
  '& a.above:hover': { textDecoration: 'underline' },
  '& .sub': { color: t.textFaint, fontSize: t.textXs, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& .state': { marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: '6px', flex: 'none', color: t.textFaint, fontSize: t.textXs },
  '& .state > i': { width: '7px', height: '7px', borderRadius: '50%' },
  '& .state[data-state="working"]': { color: t.accentText },
  '& .state[data-state="waiting-on-you"]': { color: t.warning, fontWeight: '500' },
  '& .state[data-state="error"]': { color: t.danger },
  '& .line': { color: t.textMuted, lineHeight: '1.45', overflowWrap: 'anywhere' },
  '& .key': { fontFamily: t.mono, fontSize: t.textXs, color: t.text },
  // the way to the Inbox is a line of its own
  '& a.stretch[data-testid="agent-inbox"]': { display: 'block', marginTop: '2px', color: t.warning },
  '& .bar': { height: '3px', borderRadius: '2px', background: t.bgActive },
  '& .bar > div': { height: '3px', borderRadius: '2px' },
  '& .chips': { display: 'flex', flexWrap: 'wrap', gap: '6px', alignItems: 'center' },
  '& .chip': { display: 'inline-flex', alignItems: 'center', gap: '4px', height: '20px', padding: '0 7px', borderRadius: t.rSm, background: t.bgActive, color: t.textMuted, fontSize: t.textXs, whiteSpace: 'nowrap' },
});

const dialogStack = css({ display: 'grid', gap: t.s4 });
const divider = css({ height: '1px', background: t.border, margin: '0' });

/** What a card needs beyond the agent itself. */
interface CardData {
  agent: Agent;
  state: CardState;
  runtime: string;
  local: boolean;
  line: LinePart[];
  progress?: { value: number; max: number; percent: number; key: string };
  skills: string[];
  rooms: { name: string; kind: string }[];
}

const MAX_CHIPS = 4;

function AgentCard(handle: Handle<{ d: CardData; onOpen: (a: Agent) => void }>) {
  return () => {
    const { d, onOpen } = handle.props;
    const a = d.agent;
    const waiting = d.state === 'waiting-on-you';
    const href = routes.agent.href({ id: a.id });
    const chips = [...d.skills.map((n) => ({ kind: 'skill', name: n, icon: undefined as string | undefined })), ...d.rooms.map((r) => ({ kind: 'room', name: r.name, icon: r.kind }))];
    const shown = chips.slice(0, MAX_CHIPS);
    const color = d.local ? t.success : t.accent;
    const open = on<HTMLAnchorElement, 'click'>('click', () => onOpen(a));
    return (
      <article mix={cardCss} data-testid="agent" data-name={a.name} data-agent={a.id} data-state={d.state} aria-label={a.name}>
        <div class="top">
          <Avatar id={a.id} name={a.name} size={30} kind="agent" />
          <div class="who">
            {waiting ? (
              <a class="name above" href={href} data-testid="agent-link" mix={open}>
                {a.name}
              </a>
            ) : (
              <a class="name stretch" href={href} data-testid="agent-link" mix={open}>
                {a.name}
              </a>
            )}
            <span class="sub" data-testid="agent-runs" title={[a.role ? `Role: ${a.role}` : '', runsWhereTitle(a) ?? ''].filter(Boolean).join('. ') || undefined}>
              {d.runtime} · {a.model || 'default model'}
            </span>
          </div>
          <span class="state" data-state={d.state} data-testid="presence">
            {d.state === 'working' ? <i aria-hidden="true" style={{ background: color }} /> : null}
            {CARD_WORDS[d.state]}
          </span>
        </div>
        <span class="line" data-testid="agent-doing">
          {d.line.map((p, i) =>
            typeof p === 'string' ? (
              p
            ) : 'key' in p ? (
              <span key={i} class="key" data-testid="agent-task">
                {p.key}
              </span>
            ) : (
              <a key={i} class="stretch" href={routes.needsYou.href()} data-testid="agent-inbox">
                {p.inbox}
              </a>
            ),
          )}
        </span>
        {d.state === 'working' && d.progress ? (
          <div
            class="bar"
            role="progressbar"
            aria-label={`${d.progress.key}: step ${d.progress.value} of ${d.progress.max}`}
            aria-valuemin={0}
            aria-valuemax={d.progress.max}
            aria-valuenow={d.progress.value}
            data-testid="agent-progress"
          >
            <div style={{ width: `${d.progress.percent}%`, background: color }} />
          </div>
        ) : null}
        {skipsApprovals(a.permissionMode) || (d.state !== 'working' && shown.length > 0) ? (
          <div class="chips">
            <SkipsBadge mode={a.permissionMode} />
            {d.state !== 'working'
              ? shown.map((c) => (
                  <span key={`${c.kind}:${c.name}`} class="chip" data-testid="agent-chip" data-kind={c.kind}>
                    {c.icon ? <Icon icon={roomIcon(c.icon)} size={12} /> : null}
                    {c.name}
                  </span>
                ))
              : null}
            {d.state !== 'working' && chips.length > shown.length ? (
              <span class="chip" data-testid="agent-chip" data-kind="more">
                +{chips.length - shown.length} more
              </span>
            ) : null}
          </div>
        ) : null}
      </article>
    );
  };
}

type Scope = 'project' | 'all';

/**
 * The Crew page of one project (`scope` project: the open one) or of every project (`all`). `now` replaces the clock and
 * `onNavigate` the router's navigate (tests).
 */
export function CrewScreen(handle: Handle<{ scope: Scope; now?: () => number; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.project);
  watch(handle, s.workspace);
  const clock = () => (handle.props.now ? handle.props.now() : Date.now());
  const all = () => handle.props.scope === 'all';
  const scopeId = () => (all() ? '' : (s.workspace.projectId ?? ''));
  const projectIds = (): string[] => (all() ? s.workspace.projectList.map((p) => p.id) : scopeId() ? [scopeId()] : []);
  let now = clock();
  let newOpen = false;
  let templatesOpen = false;
  let pickProject = '';

  // the workspace's agents and their presence (one project's come from the project model the shell keeps)
  const allAgents = query(handle, { load: () => s.client.agentListAll(), when: (e) => e.type.startsWith('agent.'), enabled: all });
  const allPresence = query(handle, {
    load: () => s.client.presenceList(),
    when: (e) => e.type.startsWith('room.presence_changed') || e.type.startsWith('agent.') || e.type.startsWith('watchdog.'),
    enabled: all,
  });
  const timeline = query(handle, {
    load: (): Promise<CrewTimeline> => s.client.agentTimeline(localMidnight(clock()), 0, scopeId()),
    when: (e) => e.type.startsWith('agent.') || e.type.startsWith('run.') || e.type.startsWith('approval.'),
    key: () => [scopeId(), localMidnight(now)],
    enabled: () => all() || !!scopeId(),
  });
  const summaries = query(handle, {
    load: () => orElse(() => s.client.taskSummaries({ projectId: scopeId() }).then((r) => r.summaries), [] as TaskSummary[]),
    when: (e) => e.type.startsWith('task.') || e.type.startsWith('agent.'),
    key: scopeId,
    enabled: () => all() || !!scopeId(),
  });
  const skills = query(handle, {
    load: async () => (await Promise.all(projectIds().map((id) => orElse(() => s.client.skillList(id), [] as Skill[])))).flat(),
    when: (e) => e.type.startsWith('skill.') || e.type.startsWith('agent.'),
    key: projectIds,
  });
  // the project's channels and groups with their agents (task rooms and direct messages are not "its rooms")
  const rooms = query(handle, {
    load: async () => {
      const out: CrewRoom[] = [];
      for (const pid of projectIds()) {
        const list = (await orElse(() => s.client.roomList(pid), [])).filter((r) => !r.archived && (r.kind === 'channel' || r.kind === 'group'));
        const members = await Promise.all(list.map((r) => orElse(() => s.client.roomMembers(r.id), [])));
        list.forEach((r, i) => out.push({ name: r.name, kind: r.kind, agents: (members[i] ?? []).filter((m) => m.member.kind === 'agent').map((m) => m.member.id) }));
      }
      return out;
    },
    when: (e) => e.type.startsWith('room.created') || e.type.startsWith('room.archived') || e.type.startsWith('room.member'),
    key: projectIds,
  });
  const local = query(handle, {
    load: () => orElse(async () => {
      const v = await s.client.localGet();
      return { ids: (v.usedBy ?? []).map((u) => u.agentId), providerId: v.settings?.providerId ?? '' };
    }, { ids: [] as string[], providerId: '' }),
    when: (e) => e.type === 'local.changed',
  });
  const adapters = query(handle, { load: () => orElse(() => s.client.adapterList(), [] as AdapterInfo[]), when: (e) => e.type.startsWith('connection.') || e.type.startsWith('adapter.') });
  const conns = query(handle, { load: () => orElse(() => s.client.connectionList(), [] as Connection[]), when: (e) => e.type.startsWith('connection.') });
  // every project's run mode in one request (project.runModes), however many projects there are; a failed read leaves an empty
  // answer, and an agent that follows its project is then drawn as on Windows
  const runModes = query(handle, {
    load: async () => new Map(Object.entries(await orElse(() => s.client.projectRunModes(), {} as Record<string, ProjectRunMode>))),
    when: (e) => e.type.startsWith('project.'),
  });

  // "now" moves every minute (and the timeline is read again after midnight: its key is the day)
  const timer = setInterval(() => {
    now = clock();
    void handle.update();
  }, MINUTE);
  handle.signal.addEventListener('abort', () => clearInterval(timer));

  const openNew = () => {
    newOpen = true;
    void handle.update();
  };

  // N opens New agent, outside text fields and widgets that own their keys, with no other dialog open
  window.addEventListener(
    'keydown',
    (e) => {
      if (e.key.toLowerCase() !== 'n' || e.ctrlKey || e.metaKey || e.altKey || e.shiftKey || e.repeat) return;
      if (ownsKeys(e.target) || typing(e.target) || s.ui.paletteOpen || newOpen || templatesOpen || document.querySelector('dialog[open]')) return;
      e.preventDefault();
      openNew();
    },
    { signal: handle.signal },
  );

  const reloadAgents = () => {
    void s.project.agents.reload();
    if (all()) void allAgents.reload();
  };

  /** The project a new agent or template joins: the one chosen in the dialog, else the open one, else the first. */
  const targetProject = (projects: Project[]): string => {
    if (!all()) return scopeId();
    if (projects.some((p) => p.id === pickProject)) return pickProject;
    return projects.find((p) => p.id === s.workspace.projectId)?.id ?? projects[0]?.id ?? '';
  };

  const projectPicker = (projects: Project[], value: string) =>
    all() ? (
      <Field label="Project">
        {(id) => (
          <Select id={id} value={value} options={projects.map((p) => ({ value: p.id, label: p.name }))} onChange={(v) => ((pickProject = v), void handle.update())} testId="crew-project-pick" />
        )}
      </Field>
    ) : null;

  return () => {
    now = Math.max(now, clock());
    const ws = s.workspace;
    const projects = ws.projectList;
    const isAll = all();
    const agentsQ = isAll ? allAgents : s.project.agents;
    const agents: Agent[] = (isAll ? allAgents.data : s.project.agentList) ?? [];
    const presence: PresenceState[] = (isAll ? allPresence.data : s.project.presence.data) ?? [];
    const tl = timeline.data;
    const lanesById = new Map((tl?.agents ?? []).map((l) => [l.agentId, l]));
    const list = summaries.data ?? [];
    const locals = localIds(agents, local.data);
    const projectOf = (id: string) => projects.find((p) => p.id === id);
    const keyOf = (x: { seq: number; projectKey?: string; projectId?: string }) => keyText(x.seq, x.projectKey || (x.projectId ? projectOf(x.projectId)?.key : undefined));
    const waitingOn = new Set(agents.filter((a) => !!openWait(lanesById.get(a.id))).map((a) => a.id));
    const fig = crewFigures(agents, presence, waitingOn);
    const target = targetProject(projects);

    const cardOf = (a: Agent): CardData => {
      const p = presenceFor(a.id, presence);
      const lane = lanesById.get(a.id);
      const wait = openWait(lane);
      const state = cardState(p, !!wait);
      const mine = list.filter((x) => x.assignee === a.id);
      const current = currentSummary(a.id, p, mine);
      const next = nextQueued(a.id, mine, current?.taskId);
      const last = lastFinished(a.id, mine);
      const waitTask = wait?.taskId ? list.find((x) => x.taskId === wait.taskId) : undefined;
      const prog = progressOf(current);
      return {
        agent: a,
        state,
        local: locals.has(a.id),
        runtime: runtimeName(a, {
          local: locals.has(a.id),
          project: runModes.data?.get(a.projectId),
          canWSL: (adapters.data ?? []).find((x) => x.id === a.adapter)?.capabilities?.runModes?.includes('wsl') ?? false,
          installs: conns.data ?? [],
        }),
        line: agentLine({
          state,
          presence: p,
          current,
          next,
          last,
          wait,
          waitKey: wait?.taskSeq ? (waitTask ? keyOf(waitTask) : keyOf({ seq: wait.taskSeq, projectId: a.projectId })) : undefined,
          purpose: a.purpose,
          keyOf,
        }),
        progress: prog && current ? { ...prog, key: keyOf(current) } : undefined,
        skills: (skills.data ?? []).filter((k) => k.status === 'accepted' && (k.agents ?? []).includes(a.id)).map((k) => k.name),
        rooms: roomsOf(
          a.id,
          agents.filter((x) => x.projectId === a.projectId).map((x) => x.id),
          rooms.data ?? [],
        ),
      };
    };

    const onOpen = (a: Agent) => {
      // an agent of another project: its page reads the open project's crew, so open that project first
      if (a.projectId && a.projectId !== ws.projectId) s.workspace.selectProject(a.projectId);
    };

    // the lanes follow the cards: every agent of the scope, then agents removed since that ran today
    const ids = new Set(agents.map((a) => a.id));
    const lanes: TimelineLane[] = [
      ...agents.map((a) => lanesById.get(a.id) ?? ({ agentId: a.id, name: a.name, projectId: a.projectId, role: a.role, adapter: a.adapter, model: a.model, runs: [], waits: [] } as AgentLane)),
      ...(tl?.agents ?? []).filter((l) => !ids.has(l.agentId)),
    ].map((lane) => {
      const p = isAll ? projectOf(lane.projectId) : undefined;
      return { lane, project: p ? { name: p.name, color: projectColor(p.id) } : undefined };
    });
    const w = timelineWindow(tl, now);

    const grid = (as: Agent[], label: string) => (
      <ul mix={cardsCss} aria-label={label}>
        {as.map((a) => (
          <li key={a.id}>
            <AgentCard d={cardOf(a)} onOpen={onOpen} />
          </li>
        ))}
      </ul>
    );

    const groups = isAll
      ? projects.map((p) => ({ p, as: agents.filter((a) => a.projectId === p.id) })).filter((g) => g.as.length > 0)
      : [];

    return (
      <Page
        title="Crew"
        icon={Users}
        testId="crew"
        flush
        wide
        subtitle={
          agentsQ.data ? (
            <span data-testid="crew-figures">
              <span data-testid="figure-agents">
                {fig.agents} {fig.agents === 1 ? 'agent' : 'agents'}
              </span>
              {' · '}
              <span data-testid="figure-working">{fig.working} working</span>
              {' · '}
              <span data-testid="figure-waiting">{fig.waiting} waiting on you</span>
            </span>
          ) : undefined
        }
        actions={
          <>
            <Button variant="secondary" disabled={!target} aria-haspopup="dialog" mix={on('click', () => ((templatesOpen = true), void handle.update()))}>
              Templates
            </Button>
            <Button variant="primary" disabled={!target} aria-haspopup="dialog" aria-keyshortcuts="N" mix={on('click', openNew)}>
              New agent <kbd mix={kbdOnAccent}>N</kbd>
            </Button>
          </>
        }
      >
        <ErrorNote error={agentsQ.error} what="Could not list agents" />
        {agentsQ.loading && !agentsQ.data ? <Loading /> : null}
        {agentsQ.data && agents.length === 0 ? (
          <div style={{ padding: '24px' }}>
            <Empty title="No agents yet" icon={Users}>
              <p>{isAll ? 'No project has an agent yet.' : 'No agents yet.'} A task needs a worker and a different agent as reviewer.</p>
            </Empty>
          </div>
        ) : null}
        {agents.length > 0 ? (
          <>
            <ErrorNote error={timeline.error} what="Could not read today's runs" />
            <TodayTimeline lanes={lanes} window={w} now={now} keyOf={(lane, seq) => keyText(seq, projectOf(lane.projectId)?.key)} />
            <section mix={agentsCss} aria-label="Agents">
              {isAll
                ? groups.map(({ p, as }) => (
                    <section key={p.id} aria-label={p.name} data-testid="crew-project" data-project={p.id}>
                      <h2 mix={groupHead}>
                        <i class="sq" aria-hidden="true" style={{ background: projectColor(p.id) }} />
                        {p.name}
                        <span class="count">
                          {as.length} {as.length === 1 ? 'agent' : 'agents'}
                        </span>
                      </h2>
                      {grid(as, `${p.name} agents`)}
                    </section>
                  ))
                : grid(agents, 'Agents')}
            </section>
          </>
        ) : null}

        <Dialog open={newOpen} title="New agent" label="New agent" wide onClose={() => ((newOpen = false), void handle.update())}>
          {newOpen ? (
            <div mix={dialogStack} data-testid="new-agent">
              {projectPicker(projects, target)}
              {target ? (
                <AgentForm
                  key={target}
                  projectId={target}
                  adapters={sortAdapters(adapters.data ?? [])}
                  onCreated={() => {
                    newOpen = false;
                    void handle.update();
                    reloadAgents();
                  }}
                />
              ) : null}
            </div>
          ) : null}
        </Dialog>
        <Dialog open={templatesOpen} title="Templates" label="Templates" wide onClose={() => ((templatesOpen = false), void handle.update())}>
          {templatesOpen ? (
            <div mix={dialogStack} data-testid="crew-templates">
              {projectPicker(projects, target)}
              {target ? <FromTemplate key={`t:${target}`} projectId={target} onCreated={reloadAgents} /> : null}
              <div mix={divider} role="presentation" />
              {target ? <AddPrebuilt key={`p:${target}`} projectId={target} onAdded={() => reloadAgents()} /> : null}
            </div>
          ) : null}
        </Dialog>
      </Page>
    );
  };
}

/** The open project's Crew page (/team). */
export function TeamScreen(handle: Handle<{ now?: () => number; onNavigate?: (href: string) => void }>) {
  return () => <CrewScreen scope="project" now={handle.props.now} onNavigate={handle.props.onNavigate} />;
}

/** Every project's crew (/crew): the same page, the cards grouped by project. */
export function WorkspaceCrewScreen(handle: Handle<{ now?: () => number; onNavigate?: (href: string) => void }>) {
  return () => <CrewScreen scope="all" now={handle.props.now} onNavigate={handle.props.onNavigate} />;
}
