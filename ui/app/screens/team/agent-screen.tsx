// An agent's page (Crew HQ, decision P64), /team/:id: the crew list on the left (each a link to its page), then a hero (the
// big dot, the name, the purpose, chips with what it is on, its CLI and model, effort, where it runs and whether it is held
// in) and four tabs. Overview: what it is doing right now (its task, how far along, its activity), numbers counted from the
// task list, its skills with switches, its MCP tools, what the crew remembers. Skills & tools, Memory and History go deeper.
// Everything is a projection of what the core reports; nothing is estimated and nothing is invented.

import { css, navigate, on, type Handle } from 'remix/component';
import { BookOpen, MessageSquare, Pencil, Plug, Plus, ShieldAlert, ShieldCheck, Sparkles, UserX } from 'lucide';
import type { MemoryEntry, Skill } from '../../api/extTypes.ts';
import type { McpServer } from '../../api/mcpClient.ts';
import type { Agent, PresenceState, Task } from '../../api/types.ts';
import { formatDateTime, formatUsd, presenceFor } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Avatar, Badge, StatusChip, hueFor, muted, presenceColor } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Switch } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { Tabs } from '../../ui/tabs.tsx';
import { t } from '../../ui/tokens.ts';
import { byRole, onTeam, shortDescription } from '../skills/skills.ts';
import { TaskDecisions } from '../shared/explain.tsx';
import { UsageTable } from '../shared/usage.tsx';
import { AgentEditor } from './agent-forms.tsx';
import { Run } from './inspector.tsx';
import { TASK_STEPS, adapterName, agentStats, currentTask, doing, minutesText, runsWhere, stepIndex } from './team.ts';

const layoutCss = css({
  display: 'grid',
  gridTemplateColumns: '280px minmax(0, 1fr)',
  gap: t.s6,
  alignItems: 'start',
  '@media (max-width: 1000px)': { gridTemplateColumns: 'minmax(0, 1fr)' },
});

// the crew list: a card of rows, the open agent's row tinted
const crewCss = css({
  display: 'grid',
  gap: '2px',
  alignContent: 'start',
  margin: '0',
  padding: '10px',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rXl,
  background: t.bgSurface,
  '& a': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s3,
    padding: '10px',
    borderRadius: '14px',
    color: t.text,
    textDecoration: 'none',
    minWidth: '0',
  },
  '& a:hover': { background: t.bgHover, textDecoration: 'none' },
  '& a[aria-current="page"]': { background: t.bgSidebar },
  '& .who': { display: 'grid', gap: '1px', minWidth: '0', flex: '1' },
  '& .who b': { fontSize: t.textMd, fontWeight: '600', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& .who small': { color: t.textFaint, fontSize: t.textSm, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& .dot': { width: '8px', height: '8px', borderRadius: '50%', flex: 'none' },
  '& .add': {
    display: 'flex',
    alignItems: 'center',
    gap: '10px',
    marginTop: '6px',
    padding: '12px',
    borderRadius: '14px',
    border: `1.5px dashed ${t.borderStrong}`,
    color: t.textMuted,
    fontWeight: '550',
    fontSize: t.textMd,
  },
  '& .add:hover': { color: t.text, background: t.bgHover },
});

const colCss = css({ display: 'grid', gap: t.s4, minWidth: '0' });

const heroCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s6,
  padding: t.s6,
  border: `1px solid ${t.border}`,
  borderRadius: '26px',
  background: t.bgSurface,
  minWidth: '0',
  '& .who': { flex: '1', minWidth: '0' },
  '& h1': { fontSize: '30px', fontWeight: '700', letterSpacing: '-0.03em', lineHeight: '1.15', overflowWrap: 'anywhere' },
  '& .purpose': { marginTop: '4px', color: t.textMuted, fontSize: t.textMd, lineHeight: '1.5', overflowWrap: 'anywhere', whiteSpace: 'pre-wrap' },
  '& .none': { marginTop: '4px', color: t.textFaint, fontSize: t.textMd, fontStyle: 'italic' },
  '& .chips': { display: 'flex', flexWrap: 'wrap', gap: t.s2, marginTop: t.s3 },
  '& .acts': { display: 'flex', flexDirection: 'column', gap: t.s2, flex: 'none' },
  '@media (max-width: 760px)': { flexWrap: 'wrap' },
});

const chipCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  padding: '5px 11px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rFull,
  background: t.bgSurface,
  color: t.textMuted,
  fontSize: t.textSm,
  fontWeight: '550',
  textDecoration: 'none',
  '&[data-live="true"]': { background: t.successSoft, borderColor: t.success, color: t.success },
  '& i': { display: 'block', width: '7px', height: '7px', borderRadius: '50%', background: 'currentColor' },
  '&:is(a):hover': { borderColor: t.borderStrong, textDecoration: 'none' },
});

const panelCss = css({
  display: 'grid',
  gap: t.s3,
  alignContent: 'start',
  padding: '18px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rXl,
  background: t.bgSurface,
  minWidth: '0',
  '& > h2, & > h3': { display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: t.s2, fontSize: t.textLg, fontWeight: '650', letterSpacing: '-0.01em' },
  '& > h2 span, & > h3 span': { color: t.textFaint, fontSize: t.textSm, fontWeight: '500', textAlign: 'right' },
  '& .quiet': { color: t.textMuted, fontSize: t.textMd },
});

const grid2 = css({ display: 'grid', gridTemplateColumns: 'minmax(0, 1.25fr) minmax(0, 1fr)', gap: t.s4, alignItems: 'start', '@media (max-width: 1100px)': { gridTemplateColumns: 'minmax(0, 1fr)' } });

const nowCss = css({
  display: 'grid',
  gap: t.s3,
  '& .task': { fontWeight: '600', fontSize: t.textMd, color: t.text, textDecoration: 'none', overflowWrap: 'anywhere' },
  '& .task:hover': { textDecoration: 'underline' },
  '& .steps': { display: 'flex', gap: '6px' },
  '& .steps span': { flex: '1', height: '6px', borderRadius: '3px', background: t.bgActive },
  '& .steps span[data-state="done"]': { background: t.success },
  '& .steps span[data-state="current"]': { background: t.info },
  '& .labels': { display: 'flex', justifyContent: 'space-between', color: t.textFaint, fontSize: t.textXs },
  '& .labels span': { flex: '1', textAlign: 'center' },
  // the activity block: the dark card of the design on the light theme; the sidebar's shade at night
  '& .out': {
    margin: '0',
    padding: '12px 14px',
    borderRadius: '14px',
    background: `light-dark(${t.accent}, ${t.bgSidebar})`,
    color: `light-dark(${t.onAccent}, ${t.text})`,
    fontFamily: t.mono,
    fontSize: t.textSm,
    lineHeight: '1.7',
    whiteSpace: 'pre-wrap',
    overflowWrap: 'anywhere',
    border: `1px solid light-dark(${t.accent}, ${t.border})`,
  },
  '& .out i': { fontStyle: 'normal' },
});

const statsCss = css({ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px' });
const statCss = css({
  padding: '12px 14px',
  borderRadius: '14px',
  background: t.bgSidebar,
  '& b': { display: 'block', fontSize: '22px', fontWeight: '700', letterSpacing: '-0.02em', lineHeight: '1.15' },
  '& small': { color: t.textFaint, fontSize: t.textXs },
});

const rowsCss = css({ display: 'grid', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' });
const rowCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: '10px',
  padding: '10px 12px',
  borderRadius: '14px',
  background: t.bgSidebar,
  fontSize: t.textMd,
  minWidth: '0',
  '& .ico': { display: 'grid', placeItems: 'center', flex: 'none', width: '30px', height: '30px', borderRadius: '9px', background: t.bgSurface, border: `1px solid ${t.border}`, color: t.textMuted },
  '& .what': { flex: '1', minWidth: '0', display: 'grid', gap: '1px' },
  '& .what code': { fontSize: t.textMd, background: 'transparent', padding: '0' },
  '& .what small': { color: t.textFaint, fontSize: t.textSm, overflowWrap: 'anywhere' },
  '& .what small.desc': { color: t.textMuted },
  '& em': { marginLeft: 'auto', flex: 'none', fontStyle: 'normal', color: t.textFaint, fontSize: t.textSm },
  '& label': { position: 'absolute', width: '1px', height: '1px', overflow: 'hidden', clip: 'rect(0, 0, 0, 0)', whiteSpace: 'nowrap' },
});

const memCss = css({
  padding: '10px 12px',
  borderLeft: `3px solid ${t.mentionBorder}`,
  borderRadius: '4px 12px 12px 4px',
  background: t.mentionBg,
  fontSize: t.textMd,
  lineHeight: '1.5',
  '& b': { display: 'block', fontWeight: '600' },
  '& p': { margin: '0' },
  '& small': { display: 'block', marginTop: '4px', color: t.textFaint, fontSize: t.textSm },
});

const taskList = css({ display: 'grid', gap: t.s2, margin: '0', padding: '0', listStyle: 'none' });
const taskRow = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s3,
  padding: '10px 14px',
  borderRadius: t.rLg,
  border: `1px solid ${t.border}`,
  background: t.bgSurface,
  color: t.text,
  textDecoration: 'none',
  minWidth: '0',
  '&:hover': { borderColor: t.borderStrong, textDecoration: 'none' },
  '& .seq': { color: t.textFaint, fontVariantNumeric: 'tabular-nums', flex: 'none' },
  '& .title': { flex: '1', minWidth: '0', fontWeight: '550', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& .as': { color: t.textFaint, fontSize: t.textSm, flex: 'none' },
});
const moreLink = css({ display: 'inline-flex', alignItems: 'center', gap: '4px', fontSize: t.textSm, fontWeight: '550', color: t.textMuted, '&:hover': { color: t.text } });

type TabName = 'overview' | 'skills' | 'memory' | 'history';

/** The tint behind an agent's hero: its own hue, mixed lightly into the card. Not a hex value: the hue is the agent's. */
function heroTint(id: string): string {
  const hue = hueFor(id);
  return `linear-gradient(135deg, color-mix(in oklab, hsl(${hue} 85% 70%) 24%, ${t.bgSurface}), ${t.bgSurface} 58%)`;
}

/** The chip of what an agent is on: its presence, with the task's number when it is on one. */
export function nowChip(p: PresenceState | undefined, task: Task | undefined): string {
  if (task) return p ? `${p.presence} on #${task.seq}` : `On #${task.seq}`;
  return doing(p);
}

/** The project's skills an agent receives (accepted ones that reach it), and the project's accepted skills in all. */
export function skillsOf(all: readonly Skill[], agent: Pick<Agent, 'id' | 'role'>, team: readonly { id: string }[]): { mine: Skill[]; accepted: Skill[] } {
  const accepted = all.filter((k) => k.status === 'accepted').map((k) => onTeam(k, team));
  const mine = accepted.filter((k) => byRole(k, agent.role) || (k.agents ?? []).includes(agent.id));
  return { mine, accepted };
}

/** The project's MCP servers this agent is granted. */
export const serversOf = (servers: readonly McpServer[], agentId: string): McpServer[] => servers.filter((sv) => sv.agents.some((x) => x.agentId === agentId));

function SkillRows(handle: Handle<{ skills: readonly Skill[]; agent: Agent; busy: string; onToggle: (k: Skill, on: boolean) => void; full?: boolean }>) {
  return () => {
    const { skills, agent, busy, onToggle, full } = handle.props;
    return (
      <ul mix={rowsCss} aria-label="Skills">
        {skills.map((k) => {
          const included = byRole(k, agent.role);
          const given = (k.agents ?? []).includes(agent.id);
          const on = included || given;
          return (
            <li key={k.name} mix={rowCss} data-testid="agent-skill" data-skill={k.name} data-on={on ? 'true' : 'false'}>
              <span class="ico" aria-hidden="true">
                <Icon icon={BookOpen} size={15} />
              </span>
              <span class="what">
                <code>{k.name}</code>
                {k.description ? <small class="desc">{full ? k.description : shortDescription(k.description)}</small> : null}
                {included ? <small>{(k.roles ?? []).length ? `Every ${(k.roles ?? []).join(', ')} agent gets it` : 'The whole team gets it'}</small> : null}
              </span>
              <Switch label={`${k.name} for ${agent.name}`} checked={on} disabled={included || busy === k.name} onChange={(v) => onToggle(k, v)} />
            </li>
          );
        })}
      </ul>
    );
  };
}

function ToolRows(handle: Handle<{ servers: readonly McpServer[]; agentId: string; full?: boolean }>) {
  return () => {
    const { servers, agentId, full } = handle.props;
    return (
      <ul mix={rowsCss} aria-label="MCP servers">
        {servers.map((sv) => {
          const access = sv.agents.find((x) => x.agentId === agentId);
          const tools = access?.tools.length ? access.tools.length : sv.tools.length;
          return (
            <li key={sv.id} mix={rowCss} data-testid="agent-tool" data-server={sv.id}>
              <span class="ico" aria-hidden="true">
                <Icon icon={Plug} size={15} />
              </span>
              <span class="what">
                <span>{sv.display || sv.id}</span>
                <small>
                  {tools} {tools === 1 ? 'tool' : 'tools'}
                  {access?.tools.length ? ' granted' : ''}
                  {full && access?.tools.length ? `: ${access.tools.join(', ')}` : ''}
                </small>
              </span>
              <em>{sv.enabled ? 'on' : 'off in this project'}</em>
            </li>
          );
        })}
      </ul>
    );
  };
}

function MemoryNotes(handle: Handle<{ entries: readonly MemoryEntry[]; limit?: number }>) {
  return () => {
    const { entries, limit } = handle.props;
    const shown = limit ? entries.slice(0, limit) : entries;
    return (
      <div style={{ display: 'grid', gap: '8px' }}>
        {shown.map((e) => (
          <div key={e.id} mix={memCss} data-testid="agent-memory">
            <b>{e.title}</b>
            {limit ? <p>{shortDescription(e.body, 160)}</p> : <Markdown source={e.body} />}
            <small>
              {e.kind.replace(/_/g, ' ')}
              {e.source ? ` · from ${e.source}` : ''}
            </small>
          </div>
        ))}
      </div>
    );
  };
}

/** `onNavigate` replaces the router's navigate (tests). */
export function AgentScreen(handle: Handle<{ id: string; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  watch(handle, s.project);
  watch(handle, s.workspace);
  // two actions, so a failed direct message and a failed skill switch each say what they were
  const dm = action(handle);
  const change = action(handle);
  let tab: TabName = 'overview';
  let editing = false;
  const pid = () => s.workspace.projectId ?? '';

  const detail = query(handle, {
    load: () => s.client.agentGet(handle.props.id),
    key: () => handle.props.id,
    when: (e) => e.agentId === handle.props.id || (e.type.startsWith('task.') && !e.agentId) || e.type === 'room.message_posted',
    debounceMs: 200,
  });
  const skills = query(handle, { load: () => s.client.skillList(pid()), when: (e) => e.type.startsWith('skill.') || e.type.startsWith('agent.'), key: pid, enabled: () => !!pid() });
  const servers = query(handle, { load: () => s.client.mcpServerList(pid()), when: (e) => e.type.startsWith('mcp.server') || e.type === 'mcp.access_changed' || e.type.startsWith('agent.'), key: pid, enabled: () => !!pid() });
  const memory = query(handle, { load: () => s.client.memoryList(pid(), 'accepted'), when: (e) => e.type.startsWith('memory.'), key: pid, enabled: () => !!pid() });

  const go = (href: string) => {
    if (handle.props.onNavigate) handle.props.onNavigate(href);
    else void navigate(href);
  };
  const message = (a: Agent) =>
    dm.run('dm', async () => {
      const room = await s.client.roomCreateDM(a.projectId || pid(), a.id);
      go(routes.room.href({ id: room.id }));
    });
  const toggleSkill = (a: Agent, k: Skill, on: boolean) =>
    change.run(k.name, async () => {
      await s.client.agentUpdate(on ? { id: a.id, addSkills: [k.name] } : { id: a.id, removeSkills: [k.name] });
      skills.reload();
    });

  return () => {
    const id = handle.props.id;
    const agents = s.project.agentList;
    const presence = s.project.presence.data ?? [];
    const tasks = s.project.taskList;
    const d = detail.data;
    const a = d?.agent ?? agents.find((x) => x.id === id);
    const p = presenceFor(id, presence);
    const runs = d?.runs ?? [];
    const usage = d?.usage ?? [];
    const task = a ? currentTask(a.id, p, runs, tasks) : undefined;
    const stats = a ? agentStats(a.id, tasks) : undefined;
    const spend = usage.reduce((sum, u) => sum + u.costUsd, 0);
    const sk = a ? skillsOf(skills.data ?? [], a, agents) : { mine: [], accepted: [] };
    const tools = serversOf(servers.data ?? [], id);
    const notes = memory.data ?? [];
    const iso = d?.isolation;
    const where = a ? runsWhere(a) : '';
    const mine = tasks.filter((x) => x.assignee === id || x.reviewer === id);
    const latestTask = runs.find((r) => r.taskId)?.taskId;

    const crew = (
      <nav aria-label="Crew">
        <ul mix={crewCss}>
          {agents.map((x) => {
            const px = presenceFor(x.id, presence);
            return (
              <li key={x.id}>
                <a href={routes.agent.href({ id: x.id })} aria-current={x.id === id ? 'page' : undefined} data-testid="crew-link" data-name={x.name}>
                  <Avatar id={x.id} name={x.name} size={36} kind="agent" status={presenceColor(px)} />
                  <span class="who">
                    <b>{x.name}</b>
                    <small>{x.role}</small>
                  </span>
                  <span class="dot" aria-hidden="true" style={{ background: presenceColor(px) }} />
                  <span class="sr-only">{doing(px)}</span>
                </a>
              </li>
            );
          })}
          <li>
            <a href={routes.team.href()} class="add">
              <Icon icon={Plus} size={16} />
              Add an agent
            </a>
          </li>
        </ul>
      </nav>
    );

    const stat = (value: string, label: string, testId: string) => (
      <div mix={statCss} data-testid={testId}>
        <b>{value}</b>
        <small>{label}</small>
      </div>
    );

    const overview = a && stats ? (
      <div mix={grid2}>
        <section mix={[panelCss, nowCss]} aria-label="Right now" data-testid="agent-now">
          <h2>
            Right now <span>{p ? p.presence : 'no presence reported'}</span>
          </h2>
          {task ? (
            <>
              <a href={routes.task.href({ id: task.id })} class="task" data-testid="agent-now-task">
                #{task.seq} {task.title}
              </a>
              <div class="steps" aria-hidden="true">
                {TASK_STEPS.map((st, i) => {
                  const at = stepIndex(task.status);
                  return <span key={st.key} data-state={i < at || (i === at && task.status === 'COMPLETED') ? 'done' : i === at ? 'current' : undefined} />;
                })}
              </div>
              <div class="labels" aria-hidden="true">
                {TASK_STEPS.map((st) => (
                  <span key={st.key}>{st.label}</span>
                ))}
              </div>
              <div>
                <StatusChip status={task.status} />
              </div>
            </>
          ) : (
            <p class="quiet">Not on a task right now.</p>
          )}
          <pre class="out" data-testid="agent-activity">
            {p?.activity ? `● ${p.activity}\n` : ''}
            {runs[0] ? `${runs[0].status} · started ${formatDateTime(runs[0].startedAt)}${runs[0].error ? `\n${runs[0].error}` : ''}` : 'No run yet.'}
          </pre>
          <div mix={statsCss} data-testid="agent-stats">
            {stat(String(stats.merged), stats.merged === 1 ? 'task merged' : 'tasks merged', 'stat-merged')}
            {stat(String(stats.reviewed), stats.reviewed === 1 ? 'task reviewed' : 'tasks reviewed', 'stat-reviewed')}
            {stats.firstTime !== undefined ? stat(`${Math.round(stats.firstTime * 100)}%`, 'passed review first time', 'stat-first-time') : null}
            {stats.avgCostUsd !== undefined ? stat(formatUsd(stats.avgCostUsd), 'avg. per task', 'stat-avg-cost') : null}
            {stats.avgMinutes !== undefined ? stat(minutesText(stats.avgMinutes), 'avg. time', 'stat-avg-time') : null}
            {usage.length > 0 ? stat(formatUsd(spend), 'spent in all', 'stat-spend') : null}
          </div>
        </section>
        <div mix={colCss}>
          <section mix={panelCss} aria-label="Skills">
            <h2>
              Skills <span>{skills.data ? `${sk.mine.length} of ${sk.accepted.length} in this project` : ''}</span>
            </h2>
            <ErrorNote error={skills.error} what="Skills" />
            {skills.data && sk.accepted.length === 0 ? (
              <p class="quiet">
                No skill is accepted in this project yet. Skills come from the repository or a registry on the <a href={routes.skills.href()}>Skills</a> page.
              </p>
            ) : null}
            <SkillRows skills={sk.accepted.slice(0, 5)} agent={a} busy={change.busy} onToggle={(k, on) => void toggleSkill(a, k, on)} />
            {sk.accepted.length > 5 ? (
              <button type="button" mix={[moreLink, on('click', () => ((tab = 'skills'), void handle.update()))]} style={{ border: '0', background: 'transparent', padding: '0', minHeight: '0' }}>
                All {sk.accepted.length} skills
              </button>
            ) : null}
          </section>
          <section mix={panelCss} aria-label="Tools">
            <h2>
              Tools <span>MCP</span>
            </h2>
            <ErrorNote error={servers.error} what="MCP servers" />
            {servers.data && tools.length === 0 ? (
              <p class="quiet">
                No MCP server is granted to {a.name}. Grants are made on the project's <a href={routes.projectIntegrations.href()}>Integrations</a> page.
              </p>
            ) : null}
            <ToolRows servers={tools} agentId={a.id} />
          </section>
          <section mix={panelCss} aria-label="Remembers">
            <h2>
              Remembers <span>{memory.data ? `${notes.length} ${notes.length === 1 ? 'note' : 'notes'}` : ''}</span>
            </h2>
            <ErrorNote error={memory.error} what="Memory" />
            {memory.data && notes.length === 0 ? (
              <p class="quiet">
                Nothing accepted yet. What you accept on the <a href={routes.memory.href()}>Memory</a> page reaches every agent of the crew.
              </p>
            ) : null}
            <MemoryNotes entries={notes} limit={3} />
            {notes.length > 3 ? (
              <button type="button" mix={[moreLink, on('click', () => ((tab = 'memory'), void handle.update()))]} style={{ border: '0', background: 'transparent', padding: '0', minHeight: '0' }}>
                All {notes.length} notes
              </button>
            ) : null}
          </section>
        </div>
      </div>
    ) : null;

    const skillsTab = a ? (
      <div mix={grid2}>
        <section mix={panelCss} aria-label="Skills">
          <h2>
            Skills <span>{skills.data ? `${sk.mine.length} of ${sk.accepted.length} in this project` : ''}</span>
          </h2>
          <p class="quiet">
            A whole-team skill reaches every agent its roles take in; the switch gives this agent a skill of its own or takes it away. Who gets each skill is decided on the <a href={routes.skills.href()}>Skills</a> page.
          </p>
          <ErrorNote error={skills.error} what="Skills" />
          {skills.loading && !skills.data ? <Loading /> : null}
          {skills.data && sk.accepted.length === 0 ? <p class="quiet">No skill is accepted in this project yet.</p> : null}
          <SkillRows skills={sk.accepted} agent={a} busy={change.busy} onToggle={(k, on) => void toggleSkill(a, k, on)} full />
        </section>
        <section mix={panelCss} aria-label="Tools">
          <h2>
            Tools <span>MCP</span>
          </h2>
          <p class="quiet">
            The MCP servers this agent may call, with the tools it was granted. Grants and approvals live on the project's <a href={routes.projectIntegrations.href()}>Integrations</a> page.
          </p>
          <ErrorNote error={servers.error} what="MCP servers" />
          {servers.loading && !servers.data ? <Loading /> : null}
          {servers.data && tools.length === 0 ? <p class="quiet">No MCP server is granted to {a.name}.</p> : null}
          <ToolRows servers={tools} agentId={a.id} full />
        </section>
      </div>
    ) : null;

    const memoryTab = (
      <section mix={panelCss} aria-label="Memory">
        <h2>
          Remembers <span>{memory.data ? `${notes.length} accepted ${notes.length === 1 ? 'note' : 'notes'}` : ''}</span>
        </h2>
        <p class="quiet">
          Project memory is shared by the whole crew: decisions, conventions and known issues you accepted on the <a href={routes.memory.href()}>Memory</a> page reach every agent's session.
        </p>
        <ErrorNote error={memory.error} what="Memory" />
        {memory.loading && !memory.data ? <Loading /> : null}
        {memory.data && notes.length === 0 ? <p class="quiet">Nothing accepted yet.</p> : null}
        <MemoryNotes entries={notes} />
      </section>
    );

    const historyTab = (
      <div mix={colCss}>
        <section mix={panelCss} aria-label="Tasks">
          <h2>
            Tasks <span>{mine.length ? `${mine.length} as worker or reviewer` : ''}</span>
          </h2>
          {mine.length === 0 ? <p class="quiet">No task has been assigned to this agent.</p> : null}
          <ul mix={taskList}>
            {mine.map((x) => (
              <li key={x.id}>
                <a href={routes.task.href({ id: x.id })} mix={taskRow} data-testid="agent-task">
                  <span class="seq">#{x.seq}</span>
                  <span class="title">{x.title}</span>
                  <span class="as">{x.assignee === id ? 'worker' : 'reviewer'}</span>
                  <StatusChip status={x.status} />
                </a>
              </li>
            ))}
          </ul>
        </section>
        <section mix={panelCss} aria-label="Runs">
          <h2>
            Runs <span>{runs.length ? `${runs.length}` : ''}</span>
          </h2>
          {runs.length === 0 ? <p class="quiet">This agent has not run yet.</p> : null}
          <div style={{ display: 'grid', gap: '8px' }}>
            {runs.map((r) => (
              <Run key={r.id} r={r} />
            ))}
          </div>
        </section>
        <section mix={panelCss} aria-label="Usage">
          <h2>
            Usage <span>{usage.length ? `${formatUsd(spend)} in all` : ''}</span>
          </h2>
          <UsageTable rows={usage} />
        </section>
        {latestTask ? (
          <section mix={panelCss} aria-label="Why this model">
            <h2>Why this model? (latest task)</h2>
            <TaskDecisions taskId={latestTask} />
          </section>
        ) : null}
      </div>
    );

    return (
      <Page
        title={a?.name ?? 'Agent'}
        wide
        testId="agent-page"
        head={(titleId) => (
          <div mix={layoutCss}>
            {crew}
            <div mix={colCss}>
              {!a ? (
                <div mix={panelCss}>
                  <h1 id={titleId}>Agent</h1>
                  <ErrorNote error={detail.error} what="Could not load the agent" />
                  {detail.loading || (!detail.error && !s.project.agents.data) ? <Loading /> : null}
                  {s.project.agents.data && !detail.loading && !agents.some((x) => x.id === id) ? (
                    <Empty title="No such agent" icon={UserX}>
                      <p>
                        No agent with this id is on the crew. <a href={routes.team.href()}>Back to the crew</a>.
                      </p>
                    </Empty>
                  ) : null}
                </div>
              ) : (
                <>
                  <div mix={heroCss} style={{ background: heroTint(a.id) }} data-testid="agent-hero" data-agent={a.id}>
                    <Avatar id={a.id} name={a.name} size={96} kind="agent" status={presenceColor(p)} />
                    <div class="who">
                      <h1 id={titleId}>{a.name}</h1>
                      {a.purpose ? (
                        <p class="purpose" data-testid="agent-page-purpose">
                          {a.purpose}
                        </p>
                      ) : (
                        <p class="none">No standing instructions. Edit the agent to give it a purpose.</p>
                      )}
                      <div class="chips" data-testid="agent-chips">
                        {task ? (
                          <a href={routes.task.href({ id: task.id })} mix={chipCss} data-live={p ? 'true' : undefined} data-testid="agent-chip">
                            <i aria-hidden="true" />
                            {nowChip(p, task)}
                          </a>
                        ) : (
                          <span mix={chipCss} data-live={p && /^(working|thinking)/i.test(p.presence) ? 'true' : undefined} data-testid="agent-chip">
                            <i aria-hidden="true" />
                            {nowChip(p, undefined)}
                          </span>
                        )}
                        <span mix={chipCss} data-testid="agent-chip">
                          {adapterName(a.adapter)} · {a.model || 'default model'}
                        </span>
                        {a.effort ? (
                          <span mix={chipCss} data-testid="agent-chip">
                            effort: {a.effort}
                          </span>
                        ) : null}
                        {where ? (
                          <span mix={chipCss} data-testid="agent-chip">
                            runs {where}
                          </span>
                        ) : null}
                        {iso ? (
                          <span mix={chipCss} data-testid="agent-chip" data-isolation={iso.level} title={iso.summary}>
                            <Icon icon={iso.level === 'guarded' ? ShieldCheck : ShieldAlert} size={13} />
                            {iso.level === 'guarded' ? 'guarded' : 'not held in'}
                          </span>
                        ) : null}
                        {(a.routeModels ?? []).length > 0 ? (
                          <span mix={chipCss} data-testid="agent-chip" title={`The router chooses between ${(a.routeModels ?? []).map((m) => m.model).join(', ')} on every turn`}>
                            <Icon icon={Sparkles} size={13} />
                            routed
                          </span>
                        ) : null}
                      </div>
                    </div>
                    <div class="acts">
                      <Button variant="primary" icon={MessageSquare} disabled={dm.busy === 'dm'} mix={on('click', () => void message(a))}>
                        Message {a.name}
                      </Button>
                      <Button variant="secondary" icon={Pencil} aria-haspopup="dialog" aria-expanded={editing} mix={on('click', () => ((editing = true), void handle.update()))}>
                        Edit
                      </Button>
                      <a href={routes.team.href()} mix={buttonStyle('ghost')}>
                        All agents
                      </a>
                    </div>
                  </div>
                  <ErrorNote error={dm.error} what="Could not open a direct message" />
                  <ErrorNote error={change.error} what="Skill not changed" />
                  <ErrorNote error={detail.error} what="Could not load the agent's runs and usage" />
                  <Tabs
                    label={`${a.name}'s page`}
                    pills
                    active={tab}
                    onChange={(n) => ((tab = n as TabName), void handle.update())}
                    items={[
                      { name: 'overview', label: 'Overview', testId: 'agent-tab-overview' },
                      { name: 'skills', label: 'Skills & tools', testId: 'agent-tab-skills' },
                      { name: 'memory', label: 'Memory', testId: 'agent-tab-memory' },
                      { name: 'history', label: 'History', testId: 'agent-tab-history' },
                    ]}
                  >
                    {(name) => (name === 'overview' ? overview : name === 'skills' ? skillsTab : name === 'memory' ? memoryTab : historyTab)}
                  </Tabs>
                  <Dialog open={editing} title={`Edit ${a.name}`} label={`Edit ${a.name}`} wide onClose={() => ((editing = false), void handle.update())}>
                    {editing ? (
                      <EditWithAdapter
                        agent={a}
                        onDone={() => {
                          editing = false;
                          void handle.update();
                          s.project.agents.reload();
                          detail.reload();
                        }}
                        onCancel={() => ((editing = false), void handle.update())}
                      />
                    ) : null}
                  </Dialog>
                </>
              )}
            </div>
          </div>
        )}
      />
    );
  };
}

/** The agent editor with its adapter's capabilities (model aliases, effort levels) read on demand. */
function EditWithAdapter(handle: Handle<{ agent: Agent; onDone: () => void; onCancel: () => void }>) {
  const s = services(handle);
  const adapters = query(handle, { load: () => s.client.adapterList() });
  return () => {
    const { agent, onDone, onCancel } = handle.props;
    return (
      <div>
        {adapters.loading && !adapters.data ? <p mix={muted}>Reading the adapter's capabilities...</p> : null}
        <AgentEditor key={agent.id} agent={agent} adapter={(adapters.data ?? []).find((x) => x.id === agent.adapter)} onDone={onDone} onCancel={onCancel} />
        {adapters.error ? <Badge tone="warn">adapter capabilities not read</Badge> : null}
      </div>
    );
  };
}
