// The task page's Activity (decision P87): what each agent did on the task, turn by turn, live while it works. Read from
// task.activity, the typed vocabulary modelled on AG-UI (api/agui.gen.ts), and folded into turns (activity.ts). Each agent
// process is one block: its plan as a checklist when it made one, its newest turn open (the messages, the reasoning folded
// away, each tool call as the same card a chat shows), its earlier turns folded to one line. Everything shown is text the
// core stored (redacted, bounded); nothing is rendered as HTML except the agents' messages, through the Markdown renderer.

import { css, type Handle } from 'remix/component';
import { Check, Circle, CircleDot } from 'lucide';
import type { AguiThread, PlanEntry } from '../../api/agui.gen.ts';
import type { ChatMessage } from '../../api/chatClient.ts';
import type { Agent } from '../../api/types.ts';
import { agentName } from '../../lib/format.ts';
import { query, services } from '../../state/services.tsx';
import { Avatar } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { ToolCard } from '../chats/chat-tool.tsx';
import { argsSummary, argsValue, planOf, planProgress, turnSummary, turnsOf, waitOutcomeText, type ActivityItem, type ToolItem, type Turn } from './activity.ts';

const threadCss = css({
  display: 'grid',
  gap: t.s3,
  padding: t.s4,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  '& > header': { display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textSm, color: t.textMuted, minWidth: '0' },
  '& > header strong': { color: t.text, fontWeight: '600' },
});

const planCss = css({
  display: 'grid',
  gap: '4px',
  margin: '0',
  padding: `${t.s2} ${t.s3}`,
  listStyle: 'none',
  borderRadius: t.rMd,
  background: t.bgApp,
  fontSize: t.textSm,
  '& > li': { display: 'flex', alignItems: 'flex-start', gap: t.s2, minWidth: '0', overflowWrap: 'anywhere' },
  '& > li[data-status="completed"]': { color: t.textMuted, textDecoration: 'line-through' },
  '& > li[data-status="in_progress"]': { color: t.text, fontWeight: '600' },
  '& > li[data-status="completed"] svg': { color: t.success },
  '& > li[data-status="in_progress"] svg': { color: t.accentText },
  '& > li svg': { flex: 'none', marginTop: '2px', color: t.textFaint },
  '& > .head': { color: t.textFaint, fontSize: t.textXs, fontWeight: '600', textTransform: 'uppercase', letterSpacing: '0.03em' },
});

const turnCss = css({
  display: 'grid',
  gap: '6px',
  '& > summary': { cursor: 'pointer', fontSize: t.textSm, color: t.textMuted, listStyle: 'revert' },
  '& .text': { fontSize: t.textMd, lineHeight: '1.5', maxWidth: '760px', overflowWrap: 'anywhere', '& p': { margin: '0 0 6px' } },
  '& .reasoning': { fontSize: t.textSm, color: t.textMuted, '& > summary': { cursor: 'pointer' }, '& > div': { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', padding: `4px 0 0 ${t.s3}`, borderLeft: `2px solid ${t.border}` } },
  '& .wait': { display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '6px', padding: `6px ${t.s3}`, borderRadius: t.rMd, fontSize: t.textSm, border: `1px solid ${t.border}`, overflowWrap: 'anywhere' },
  '& .wait[data-open="true"]': { borderColor: t.mentionBorder, background: t.mentionBg },
  '& .wait > strong': { fontWeight: '600' },
  '& .end': { fontSize: t.textXs, color: t.textFaint },
  '& .end[data-status="error"]': { color: t.danger },
});

/** A tool call drawn with the chat's card: the card reads a chat message, so the call is shown as one. */
function asMessage(i: ToolItem, seq: number): ChatMessage {
  return {
    id: i.id,
    chatId: '',
    seq,
    role: 'tool',
    body: '',
    toolName: i.name,
    toolInput: argsValue(i.args),
    createdAt: 0,
    toolStatus: i.result === undefined ? undefined : i.isError ? 'failed' : 'ok',
    toolResult: i.result,
  };
}

function Plan(handle: Handle<{ plan: PlanEntry[] }>) {
  return () => {
    const plan = handle.props.plan;
    return (
      <ul mix={planCss} aria-label={`Plan, ${planProgress(plan)}`} data-testid="activity-plan">
        <li class="head">Plan · {planProgress(plan)}</li>
        {plan.map((p, i) => (
          <li key={i} data-status={p.status ?? 'pending'}>
            <Icon icon={p.status === 'completed' ? Check : p.status === 'in_progress' ? CircleDot : Circle} size={14} />
            <span>{p.content}</span>
          </li>
        ))}
      </ul>
    );
  };
}

function Item(handle: Handle<{ item: ActivityItem; running: boolean; seq: number }>) {
  return () => {
    const { item, running, seq } = handle.props;
    if (item.kind === 'text') {
      return (
        <div class="text" data-testid="activity-text">
          <Markdown source={item.text} />
        </div>
      );
    }
    if (item.kind === 'wait') {
      const open = !item.outcome;
      return (
        <div class="wait" data-open={open ? 'true' : undefined} data-outcome={item.outcome} data-testid="activity-wait">
          <strong>{waitOutcomeText(item.outcome)}:</strong>
          <span>{item.title}</span>
          {open ? <span>· answer it under Needs your decision</span> : null}
        </div>
      );
    }
    if (item.kind === 'reasoning') {
      return (
        <details class="reasoning" data-testid="activity-reasoning">
          <summary>Thinking</summary>
          <div>{item.text}</div>
        </details>
      );
    }
    return <ToolCard m={asMessage(item, seq)} summary={argsSummary(item.args)} running={running && item.result === undefined} />;
  };
}

function TurnView(handle: Handle<{ turn: Turn; n: number; open: boolean }>) {
  return () => {
    const { turn, n, open } = handle.props;
    const running = turn.status === 'running';
    const body = (
      <>
        {turn.items.map((it, i) => (
          <Item key={it.id} item={it} running={running} seq={i} />
        ))}
        {turn.status === 'error' ? (
          <span class="end" data-status="error" data-testid="activity-error">
            The turn failed: {turn.error}
          </span>
        ) : turn.status === 'stopped' ? (
          <span class="end">The agent's program stopped during the turn.</span>
        ) : null}
      </>
    );
    return (
      <details mix={turnCss} open={open || undefined} data-testid="activity-turn" data-status={turn.status}>
        <summary>
          Turn {n} · {turnSummary(turn)}
        </summary>
        {body}
      </details>
    );
  };
}

export function ThreadView(handle: Handle<{ thread: AguiThread; agents: readonly Agent[] }>) {
  return () => {
    const { thread, agents } = handle.props;
    const turns = turnsOf(thread.events);
    const plan = planOf(thread.events);
    const name = thread.agentId ? agentName(thread.agentId, agents) : 'An agent';
    const last = turns[turns.length - 1];
    return (
      <article mix={threadCss} data-testid="activity-thread" data-agent={thread.agentId} aria-label={`${name}'s activity`}>
        <header>
          {thread.agentId ? <Avatar id={thread.agentId} name={name} size={20} kind="agent" /> : null}
          <strong>{name}</strong>
          <span>
            {turns.length === 1 ? '1 turn' : `${turns.length} turns`}
            {last?.status === 'running' ? ' · working now' : ''}
          </span>
        </header>
        {plan && plan.length ? <Plan plan={plan} /> : null}
        {turns.map((tn, i) => (
          <TurnView key={tn.runId || i} turn={tn} n={i + 1} open={i === turns.length - 1} />
        ))}
      </article>
    );
  };
}

/** The Activity section's body: every agent process of the task, the newest first. */
export function TaskActivity(handle: Handle<{ taskId: string; agents: readonly Agent[] }>) {
  const s = services(handle);
  const act = query(handle, {
    key: () => handle.props.taskId,
    load: () => s.client.taskActivity(handle.props.taskId),
    when: (e) => e.taskId === handle.props.taskId && e.type.startsWith('agent.'),
    noisy: true,
    debounceMs: 750,
  });
  return () => {
    const threads = [...(act.data?.threads ?? [])].reverse();
    return (
      <div style={{ display: 'grid', gap: t.s3 }} data-testid="task-activity">
        <ErrorNote error={act.error} what="Could not load the activity" />
        {act.data?.truncated ? <p style={{ margin: '0', color: t.textFaint, fontSize: t.textSm }}>Only the newest activity is shown.</p> : null}
        {act.data && !threads.length ? <p style={{ margin: '0', color: t.textMuted }}>No agent has worked on this task yet.</p> : null}
        {threads.map((th) => (
          <ThreadView key={th.threadId} thread={th} agents={handle.props.agents} />
        ))}
      </div>
    );
  };
}
