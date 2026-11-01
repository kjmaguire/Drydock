// Work (decision P141): a project's home ("/"). On the left, "Search this project" and one list of its tasks and its chats,
// grouped Needs you / Running / Earlier (work.ts), each row a status dot, the title and one faint line; under it the board, the
// pull requests and the rooms. In the centre the item open (?item=task:<id> or chat:<id>, so Reload and Back keep it): a task's
// own page (TaskDetailScreen, embedded) or the chat (ChatConversation); with none named, the first that needs you, else the first
// running, else one line and New task. On the right the crew: each agent with its model and effort and its state (a click opens
// its editor in place, P137, through edit-agent.ts), "+" for a new one, then the project's spend, its first-check rate, its security findings and what
// merged this week (the old Overview's Insights), and where the folder stands against its remote with Sync now (P143,
// repo-sync-line.tsx). Every number is the core's; nothing is decided on this page but in the item.

import { css, navigate, on, type Handle } from 'remix/component';
import { Plus, Search } from 'lucide';
import type { Chat } from '../../api/chatClient.ts';
import { agentName, presenceFor, presenceWords } from '../../lib/format.ts';
import { modelName } from '../../lib/model-name.ts';
import { parseWorkItem, workItemHref, workItemKey, type WorkItemRef } from '../../lib/route.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Avatar, presenceColor } from '../../ui/data.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { ChatConversation } from '../chats/chat-conversation.tsx';
import { WEEK_MS, firstCheckText, mergedSince, spendLine } from '../overview/overview.ts';
import { findingsText } from '../security/security.ts';
import { TaskDetailScreen } from '../tasks/task-detail-screen.tsx';
import { AgentPanelHost } from '../team/agent-panel.tsx';
import { editAgent, editAgentHref, newAgentHref } from './edit-agent.ts';
import { RepoSyncLine } from './repo-sync-line.tsx';
import { GROUP_TITLE, filterWork, groupWork, modelEffort, openItem, workRows, type WorkGroup, type WorkRow } from './work.ts';

const layoutCss = css({
  display: 'grid',
  gridTemplateColumns: '288px minmax(0, 1fr) 248px',
  height: '100%',
  minHeight: '0',
  background: t.bgApp,
  // the crew rail leaves under 1180 px (crewCss hides itself: a rule here would lose to its later layer, and the rail then
  // wrapped under the list and squeezed the centre to half its height; decision P144); the Crew tab has the crew
  '@media (max-width: 1180px)': { gridTemplateColumns: '264px minmax(0, 1fr)' },
  '@media (max-width: 760px)': { gridTemplateColumns: 'minmax(0, 1fr)', gridTemplateRows: 'minmax(0, 40%) minmax(0, 1fr)' },
});
const leftCss = css({ display: 'flex', flexDirection: 'column', minHeight: '0', borderRight: `1px solid ${t.border}`, background: t.bgSidebar });
const searchRow = css({ display: 'flex', alignItems: 'center', gap: t.s2, flex: 'none', padding: `${t.s3} ${t.s3} ${t.s2}` });
const searchBox = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flex: '1',
  minWidth: '0',
  height: '32px',
  padding: `0 ${t.s2}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  color: t.textFaint,
  '&:focus-within': { borderColor: t.accent, boxShadow: t.focus },
  '& > input': { flex: '1', minWidth: '0', height: '30px', padding: '0', border: '0', background: 'transparent', color: t.text, fontSize: t.textMd, outline: 'none', boxShadow: 'none' },
});
const listCss = css({ flex: '1', minHeight: '0', overflowY: 'auto', padding: `0 ${t.s2} ${t.s3}` });
const groupHead = css({ margin: `${t.s4} ${t.s2} ${t.s1}`, color: t.textFaint, fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.06em', textTransform: 'uppercase' });
const rowsCss = css({ display: 'grid', gap: '2px', margin: '0', padding: '0', listStyle: 'none' });
const rowCss = css({
  display: 'grid',
  gridTemplateColumns: '8px minmax(0, 1fr)',
  columnGap: t.s2,
  alignItems: 'baseline',
  padding: `6px ${t.s2}`,
  borderRadius: t.rMd,
  color: t.text,
  textDecoration: 'none',
  '& > .dot': { width: '7px', height: '7px', borderRadius: '50%', alignSelf: 'center', background: t.textFaint },
  '& > .dot[data-tone="needs"]': { background: t.warning },
  '& > .dot[data-tone="running"]': { background: t.accent },
  '& > .dot[data-tone="done"]': { background: t.success },
  '& > .dot[data-tone="failed"]': { background: t.danger },
  '& > .dot[data-tone="idle"]': { background: 'transparent', boxShadow: `inset 0 0 0 1.5px ${t.textFaint}` },
  '& > .title': { minWidth: '0', fontSize: t.textMd, fontWeight: '500', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& > .line': { gridColumn: '2', minWidth: '0', color: t.textFaint, fontSize: t.textSm, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '&:hover': { background: t.bgHover, textDecoration: 'none' },
  '&[aria-current="true"]': { background: t.bgActive },
  '&[data-group="needs"][aria-current="true"]': { background: t.warningSoft },
  '&:focus-visible': { boxShadow: `inset ${t.focus}`, outline: 'none' },
});
const flashCss = css({ flex: 'none', padding: `0 ${t.s3} ${t.s2}` });
const quietCss = css({ margin: `${t.s3} ${t.s2}`, color: t.textMuted, fontSize: t.textSm });
const footCss = css({
  display: 'flex',
  flexWrap: 'wrap',
  gap: t.s1,
  flex: 'none',
  padding: `${t.s2} ${t.s3}`,
  borderTop: `1px solid ${t.border}`,
  '& > a': { display: 'inline-flex', alignItems: 'center', minHeight: '28px', padding: `0 ${t.s2}`, borderRadius: t.rMd, color: t.textMuted, fontSize: t.textSm, textDecoration: 'none' },
  '& > a:hover': { background: t.bgHover, color: t.text },
});
const centreCss = css({ display: 'flex', flexDirection: 'column', minWidth: '0', minHeight: '0', overflow: 'hidden', '& > *': { flex: '1', minHeight: '0' } });
const emptyCss = css({ display: 'grid', placeItems: 'center', alignContent: 'center', gap: t.s3, padding: t.s6, color: t.textMuted, fontSize: t.textMd, textAlign: 'center' });
const crewCss = css({
  display: 'flex',
  flexDirection: 'column',
  minHeight: '0',
  overflowY: 'auto',
  padding: `${t.s3} ${t.s3}`,
  borderLeft: `1px solid ${t.border}`,
  '@media (max-width: 1180px)': { display: 'none' },
});
const crewHead = css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', margin: `0 ${t.s1} ${t.s2}`, fontSize: t.textMd, fontWeight: '600' });
const plusCss = css({ display: 'inline-grid', placeItems: 'center', width: '28px', height: '28px', borderRadius: t.rMd, color: t.textMuted, '&:hover': { background: t.bgHover, color: t.text } });
const agentCss = css({
  display: 'grid',
  gridTemplateColumns: 'auto minmax(0, 1fr)',
  columnGap: t.s2,
  alignItems: 'center',
  padding: `6px ${t.s1}`,
  borderRadius: t.rMd,
  color: t.text,
  textDecoration: 'none',
  '& > span:first-child': { gridRow: 'span 2' },
  '& > .name': { minWidth: '0', fontSize: t.textMd, fontWeight: '500', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& > .line': { minWidth: '0', color: t.textFaint, fontSize: t.textSm, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '&:hover': { background: t.bgHover, textDecoration: 'none' },
});
const statsCss = css({
  display: 'grid',
  gap: '2px',
  margin: `${t.s3} 0 0`,
  padding: `${t.s3} 0 0`,
  borderTop: `1px solid ${t.border}`,
  '& > a': { display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: t.s2, minHeight: '28px', padding: `0 ${t.s1}`, borderRadius: t.rMd, color: t.textMuted, fontSize: t.textSm, textDecoration: 'none' },
  '& > a > b': { color: t.text, fontWeight: '500', fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' },
  '& > a:hover': { background: t.bgHover, color: t.text },
});

export interface WorkScreenProps {
  /** The ?item= value: "task:<id>" or "chat:<id>". */
  item?: string;
  /** Replaces the router's navigate (tests). */
  onNavigate?: (href: string) => void;
}

export function WorkScreen(handle: Handle<WorkScreenProps>) {
  const s = services(handle);
  watch(handle, s.workspace);
  watch(handle, s.project);
  const pid = () => s.workspace.projectId;
  const on_ = { key: pid, enabled: () => !!pid() };
  const chats = query(handle, { load: (): Promise<Chat[]> => s.client.chatList({ projectId: pid()! }), when: (e) => e.type.startsWith('chat.'), ...on_ });
  const budget = query(handle, { load: () => s.client.budgetStatus({ projectId: pid()! }), when: (e) => e.type.startsWith('budget.') || e.type.startsWith('task.'), debounceMs: 500, ...on_ });
  const evals = query(handle, { load: () => s.client.evalsReport({ projectId: pid()!, sinceMs: Date.now() - WEEK_MS }), when: (e) => e.type.startsWith('task.'), debounceMs: 500, ...on_ });
  const scan = query(handle, { load: () => s.client.securityScanGet(pid()!), when: (e) => e.type.startsWith('security.'), ...on_ });
  const act = action(handle);
  // a note the page before left (what a start from Home could not do, P142), shown once as one line
  const flash = s.ui.takeFlash();
  let search = '';
  const go = (href: string) => (handle.props.onNavigate ? handle.props.onNavigate(href) : void navigate(href).catch(() => {}));

  // a new chat in this project, made at once with its CLI's defaults and opened in the centre
  const newChat = () =>
    act.run('chat', async () => {
      const c = await s.client.chatCreate({ projectId: pid() });
      go(workItemHref({ kind: 'chat', id: c.id }));
    });

  const row = (r: WorkRow, open: string | undefined) => (
    <li key={r.key}>
      <a
        href={workItemHref(r.ref)}
        mix={rowCss}
        data-testid="work-item"
        data-kind={r.ref.kind}
        data-group={r.group}
        data-item={r.key}
        aria-current={r.key === open ? 'true' : undefined}
      >
        <span class="dot" data-tone={r.tone} aria-hidden="true" />
        <span class="title">{r.title}</span>
        <span class="line">{r.line}</span>
      </a>
    </li>
  );

  return () => {
    const p = s.project;
    const agents = p.agentList;
    const presence = p.presence.data;
    const rows = workRows({ tasks: p.taskList, chats: chats.data ?? [], needs: p.needs.data ?? [], agentName: (id) => agentName(id, agents) });
    const shown = filterWork(rows, search);
    const groups = groupWork(shown);
    const named = parseWorkItem(handle.props.item);
    const open: WorkItemRef | undefined = openItem(rows, named);
    const openKey = open ? workItemKey(open) : undefined;
    const spend = budget.data ? spendLine(budget.data.usage['project'], budget.data.statuses) : undefined;
    const first = evals.data ? firstCheckText(evals.data.report) : undefined;
    const lastScan = scan.data && scan.data.status !== 'running' ? scan.data : undefined;
    const merged = mergedSince(p.taskList, Date.now() - WEEK_MS);
    const channels = p.roomList.filter((r) => r.kind === 'channel' && !r.archived);
    const general = channels.find((r) => r.name === 'general') ?? channels[0];

    return (
      <div mix={layoutCss} data-testid="work">
        <h1 class="sr-only">Work</h1>
        <section mix={leftCss} aria-label="Tasks and chats">
          <div mix={searchRow}>
            <label mix={searchBox}>
              <Icon icon={Search} size={14} />
              <input
                type="search"
                value={search}
                placeholder="Search this project"
                aria-label="Search this project"
                data-testid="work-search"
                mix={on('input', (e) => {
                  search = (e.currentTarget as HTMLInputElement).value;
                  void handle.update();
                })}
              />
            </label>
            <Button size="sm" variant="ghost" icon={Plus} aria-label="New chat in this project" title="New chat in this project" disabled={act.busy === 'chat'} data-testid="work-new-chat" mix={on('click', () => void newChat())}>
              Chat
            </Button>
          </div>
          {flash ? (
            <div mix={flashCss}>
              <Notice tone={flash.tone} role="status" testId="work-flash">
                {flash.text}
              </Notice>
            </div>
          ) : null}
          <ErrorNote error={act.error ?? chats.error} what={act.error ? 'Could not start a chat' : 'Could not read the chats'} />
          <div mix={listCss} data-testid="work-list">
            {(['needs', 'running', 'earlier'] as WorkGroup[]).map((g) =>
              groups[g].length ? (
                <div key={g} data-group={g}>
                  <h2 mix={groupHead}>{GROUP_TITLE[g]}</h2>
                  <ul mix={rowsCss} aria-label={GROUP_TITLE[g]}>
                    {groups[g].map((r) => row(r, openKey))}
                  </ul>
                </div>
              ) : null,
            )}
            {rows.length === 0 ? <p mix={quietCss}>No tasks or chats yet.</p> : shown.length === 0 ? <p mix={quietCss}>Nothing matches "{search}".</p> : null}
          </div>
          <nav mix={footCss} aria-label="More of this project">
            <a href={routes.tasks.href()} data-testid="nav-tasks">
              Board
            </a>
            <a href={routes.pulls.href()} data-testid="nav-pulls">
              Pull requests
            </a>
            {/* straight to the project channel when it is known: /rooms only redirects there */}
            <a href={general ? routes.room.href({ id: general.id }) : routes.rooms.href()} data-testid="nav-rooms">
              Rooms
            </a>
          </nav>
        </section>

        <div mix={centreCss} data-testid="work-centre" data-item={openKey ?? ''}>
          {open?.kind === 'task' ? (
            <TaskDetailScreen key={open.id} id={open.id} embedded />
          ) : open?.kind === 'chat' ? (
            <ChatConversation key={open.id} chatId={open.id} onNavigate={handle.props.onNavigate} />
          ) : (
            <div mix={emptyCss} data-testid="work-empty">
              <p>Describe a task and the crew takes it from there.</p>
              <a href={routes.taskNew.href()} mix={buttonStyle('primary')}>
                Describe a task
              </a>
            </div>
          )}
        </div>

        <aside mix={crewCss} aria-label="Crew" data-crew="">
          <div mix={crewHead}>
            <span>Crew</span>
            <a href={newAgentHref()} mix={plusCss} aria-label="Add an agent" title="Add an agent" data-testid="work-add-agent">
              <Icon icon={Plus} size={15} />
            </a>
          </div>
          {agents.length === 0 ? <p mix={quietCss}>No agents yet.</p> : null}
          {agents.map((a) => {
            const pr = presenceFor(a.id, presence);
            return (
              <a
                key={a.id}
                href={editAgentHref(a.id)}
                mix={[
                  agentCss,
                  on('click', (e) => {
                    // a plain click edits in place; a modified one keeps the link's own way (a new window)
                    if (e.ctrlKey || e.metaKey || e.shiftKey || e.button !== 0) return;
                    e.preventDefault();
                    editAgent(a.id);
                  }),
                ]}
                data-testid="work-agent"
                data-name={a.name}
                title={`Edit ${a.name}`}
              >
                <Avatar id={a.id} name={a.name} size={28} kind="agent" status={presenceColor(pr)} />
                <span class="name">
                  {a.name}
                  <span class="sr-only">, {presenceWords(pr)}</span>
                </span>
                <span class="line">{modelEffort(a.model, a.effort, modelName)}</span>
              </a>
            );
          })}
          <div mix={statsCss} data-testid="work-stats">
            <a href={routes.budgets.href()} data-testid="work-spend" title={spend?.note}>
              Spent so far <b>{spend ? spend.amount : '…'}</b>
            </a>
            <a href={routes.evals.href()} data-testid="work-checks" title="Tasks whose first check passed, last 7 days">
              First check <b>{first ?? '…'}</b>
            </a>
            <a href={routes.security.href()} data-testid="work-security">
              Security <b>{scan.data === undefined ? '…' : lastScan ? findingsText(lastScan.counts.total) : scan.data ? 'scanning' : 'not scanned'}</b>
            </a>
            <a href={routes.tasks.href()} data-testid="work-merged">
              Merged this week <b>{merged}</b>
            </a>
            {s.workspace.project?.hasRepo && pid() ? <RepoSyncLine key={pid()} projectId={pid()!} /> : null}
          </div>
        </aside>
        <AgentPanelHost agents={agents} />
      </div>
    );
  };
}
