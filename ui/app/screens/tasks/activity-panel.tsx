// The task page's Activity (decisions P87, P97: the graphite mockup "Task"): what each agent did on the task, step by step, live
// while it works. Read from task.activity, the typed vocabulary modelled on AG-UI (api/agui.gen.ts), folded into turns
// (activity.ts) and read as compact steps (activity-steps.ts): a 22 px tile per step, "Read parse.go parse_test.go", "Edited
// parse.go +14 −3" with the first lines of the change, "Ran go test ./... · 1 failed" with the failing line, "Thought for 6 s",
// the running step with a spinner and its seconds, each with its time on the right. A step opens to the call's whole input and
// result. Each agent process is a line with its CLI and model, its plan as a checklist when it made one; its newest turn is
// open, earlier turns are one line each. The feed is windowed (VirtualList): only the rows near the view are in the page, and
// it stays at the bottom while the reader is there. Everything shown is text the core stored (redacted, bounded); nothing is
// rendered as HTML except the agents' messages, through the Markdown renderer.

import { css, on, ref, type Handle } from 'remix/component';
import { Ban, Check, ChevronRight, Circle, CircleDot, FileText, Hand, Lightbulb, MessageSquare, PenLine, Search, SquareTerminal, Wrench, X } from 'lucide';
import type { AguiThread, PlanEntry } from '../../api/agui.gen.ts';
import type { Agent, Connection } from '../../api/types.ts';
import { agentName } from '../../lib/format.ts';
import { query, services } from '../../state/services.tsx';
import { Avatar } from '../../ui/data.tsx';
import { ErrorNote, Spinner } from '../../ui/feedback.tsx';
import { Icon, type IconNode } from '../../ui/icon.tsx';
import { Markdown } from '../../ui/markdown.tsx';
import { t } from '../../ui/tokens.ts';
import { VirtualList } from '../../ui/virtual-list.tsx';
import { toolInputText } from '../chats/chats.ts';
import { cliName } from '../shared/cli-name.ts';
import { argsValue, planOf, planProgress, turnSummary, waitOutcomeText, type ToolItem, type Turn } from './activity.ts';
import { activityRows, baseName, changeLines, secondsText, stepVerb, timeOfDay, type ActivityRow, type ActivityStep } from './activity-steps.ts';

const sectionCss = css({
  display: 'grid',
  gap: t.s3,
  minWidth: '0',
  '& > .head': { display: 'flex', alignItems: 'center', gap: '10px', minWidth: '0', color: t.textFaint, fontSize: t.textXs, fontWeight: '500' },
  '& > .head > h2': { margin: '0', fontSize: t.textXs, fontWeight: '500', color: t.textFaint },
  '& > .head > .rule': { flex: '1', minWidth: '16px', height: '1px', background: t.border },
  '& > .head > .who': { minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& > .note': { margin: '0', color: t.textFaint, fontSize: t.textSm },
});

// the feed's own scroller: as tall as its rows up to most of the window, then it scrolls (and keeps to the bottom while read there)
const feedCss = css({ maxHeight: 'min(70vh, 720px)', overflowY: 'auto', overflowX: 'hidden', minWidth: '0', overscrollBehavior: 'contain', '&:focus-visible': { outline: 'none', boxShadow: t.focus } });

const rowCss = css({ paddingBottom: '14px', minWidth: '0' });

const threadCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  minWidth: '0',
  color: t.textMuted,
  fontSize: t.textSm,
  '& strong': { color: t.text, fontWeight: '500' },
  '& .meta': { minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: t.textFaint },
});

const planCss = css({
  display: 'grid',
  gap: '4px',
  margin: '0 0 0 34px',
  padding: `${t.s2} ${t.s3}`,
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  fontSize: t.textSm,
  '& > li': { display: 'flex', alignItems: 'flex-start', gap: t.s2, minWidth: '0', overflowWrap: 'anywhere' },
  '& > li[data-status="completed"]': { color: t.textMuted, textDecoration: 'line-through' },
  '& > li[data-status="in_progress"]': { color: t.text, fontWeight: '500' },
  '& > li[data-status="completed"] svg': { color: t.success },
  '& > li[data-status="in_progress"] svg': { color: t.accentText },
  '& > li svg': { flex: 'none', marginTop: '2px', color: t.textFaint },
  '& > .head': { color: t.textFaint, fontSize: t.textXs, fontWeight: '500' },
});

const turnCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  width: '100%',
  minHeight: '28px',
  padding: '0 6px 0 2px',
  border: '0',
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.textMuted,
  fontSize: t.textSm,
  textAlign: 'left',
  '&:hover': { background: t.bgHover, color: t.text },
  '& .chev': { display: 'inline-flex', width: '22px', justifyContent: 'center', flex: 'none', transition: `transform ${t.fast} ${t.ease}` },
  '&[aria-expanded="true"] .chev': { transform: 'rotate(90deg)' },
  '& .at': { marginLeft: 'auto', color: t.textFaint, fontSize: t.textXs, fontVariantNumeric: 'tabular-nums', flex: 'none' },
});

// one step: a 22 px tile and its line; the line opens to the call
const stepCss = css({
  display: 'flex',
  gap: t.s3,
  alignItems: 'flex-start',
  minWidth: '0',
  '& > .tile': {
    display: 'grid',
    placeItems: 'center',
    width: '22px',
    height: '22px',
    flex: 'none',
    borderRadius: '6px',
    background: t.bgRaised,
    border: `1px solid ${t.border}`,
    color: t.textMuted,
  },
  '& > .tile[data-tone="accent"]': { color: t.accent },
  '& > .tile[data-tone="danger"]': { color: t.danger, borderColor: `color-mix(in srgb, ${t.danger} 35%, ${t.border})` },
  '& > .tile[data-tone="ok"]': { color: t.success },
  '& > .tile[data-tone="running"]': { borderColor: `color-mix(in srgb, ${t.accent} 45%, ${t.border})` },
  '& > .main': { flex: '1', minWidth: '0', display: 'grid', gap: '6px' },
  '& .line': { display: 'flex', alignItems: 'baseline', gap: t.s2, minWidth: '0', minHeight: '22px', lineHeight: '22px' },
  '& summary.line': { cursor: 'pointer', listStyle: 'none', borderRadius: t.rSm },
  '& summary.line::-webkit-details-marker': { display: 'none' },
  '& summary.line:hover .verb': { color: t.accentText },
  '& summary.line:focus-visible': { outline: 'none', boxShadow: t.focus },
  '& .verb': { flex: 'none' },
  '& .what': { minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontFamily: t.mono, fontSize: t.textXs, color: t.textMuted },
  '& .what.more': { color: t.textFaint, flex: '0 1 auto' },
  '& .add': { flex: 'none', fontFamily: t.mono, fontSize: t.textXs, color: t.success },
  '& .del': { flex: 'none', fontFamily: t.mono, fontSize: t.textXs, color: t.danger },
  '& .bad': { flex: 'none', fontSize: t.textXs, color: t.danger },
  '& .quiet': { flex: 'none', fontSize: t.textXs, color: t.textFaint },
  '& .at': { marginLeft: 'auto', paddingLeft: t.s2, flex: 'none', color: t.textFaint, fontSize: t.textXs, fontVariantNumeric: 'tabular-nums' },
  '& .why': { fontFamily: t.mono, fontSize: t.textXs, color: t.textMuted, overflowWrap: 'anywhere', whiteSpace: 'pre-wrap' },
  '& .hunk': {
    margin: '0',
    padding: '8px 0',
    border: `1px solid ${t.border}`,
    borderRadius: t.rLg,
    background: t.bgSurface,
    fontFamily: t.mono,
    fontSize: t.textXs,
    lineHeight: '1.6',
    overflowX: 'auto',
    color: t.textMuted,
  },
  '& .hunk > div': { padding: '0 12px', whiteSpace: 'pre' },
  '& .hunk > div[data-kind="-"]': { color: t.danger, background: t.dangerSoft },
  '& .hunk > div[data-kind="+"]': { color: t.success, background: t.successSoft },
  '& .hunk > .rest': { color: t.textFaint },
  '& .body': { display: 'grid', gap: t.s2, padding: `${t.s2} 0 2px` },
  '& .body .label': { fontSize: t.textXs, color: t.textFaint, fontWeight: '500' },
  '& .body pre': { margin: '0', maxHeight: '300px', overflow: 'auto', fontSize: t.textXs, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
  '& .text': { fontSize: t.textMd, lineHeight: '1.55', overflowWrap: 'anywhere', '& p': { margin: '0 0 6px' }, '& p:last-child': { margin: '0' } },
  '& .thought > summary': { display: 'flex', alignItems: 'baseline', gap: t.s2, minHeight: '22px', lineHeight: '22px', cursor: 'pointer', listStyle: 'none', color: t.textMuted },
  '& .thought > summary::-webkit-details-marker': { display: 'none' },
  '& .thought > summary .show': { color: t.accentText, fontSize: t.textXs },
  '& .thought > div': { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', padding: `4px 0 0 ${t.s3}`, borderLeft: `2px solid ${t.border}`, color: t.textMuted, fontSize: t.textSm },
  '& .wait': { display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: '6px', minHeight: '22px', lineHeight: '22px', overflowWrap: 'anywhere' },
  '& .wait[data-open="true"] > strong': { color: t.warning },
  '& .wait > strong': { fontWeight: '500' },
  '& .end': { minHeight: '22px', lineHeight: '22px', color: t.textMuted },
  '& .end[data-status="error"]': { color: t.danger },
});

const TOOL_ICON: Record<string, IconNode> = { read: FileText, edit: PenLine, run: SquareTerminal, search: Search, tool: Wrench, text: MessageSquare, thought: Lightbulb, wait: Hand };

/** The tile of a step: a spinner while it runs, an x when it failed, else the icon of its kind. */
function Tile(handle: Handle<{ step: ActivityStep }>) {
  return () => {
    const s = handle.props.step;
    if (s.state === 'running' && s.kind !== 'text') {
      return (
        <span class="tile" data-tone="running" aria-hidden="true">
          <Spinner label="Running" />
        </span>
      );
    }
    const failed = s.state === 'failed';
    const tone = failed ? 'danger' : s.kind === 'edit' ? 'accent' : s.kind === 'wait' && !s.wait.outcome ? 'accent' : undefined;
    return (
      <span class="tile" data-tone={tone} aria-hidden="true">
        <Icon icon={failed ? X : TOOL_ICON[s.kind]!} size={12} strokeWidth={2.2} />
      </span>
    );
  };
}

/** The time on the right: the time of day it happened; for a running step how long it has run, then "now". */
function At(handle: Handle<{ step: ActivityStep; now: number }>) {
  return () => {
    const { step, now } = handle.props;
    if (step.state === 'running') {
      const ran = step.at ? secondsText(Math.max(0, now - step.at)) : '';
      return (
        <>
          {ran ? <span class="quiet">{ran}</span> : null}
          <span class="at">now</span>
        </>
      );
    }
    const at = timeOfDay(step.at);
    return at ? <time class="at">{at}</time> : null;
  };
}

/** One call's whole input and result, as the chat's tool card shows it (an edit as its lines, a command with its output). */
function CallBody(handle: Handle<{ tool: ToolItem; running: boolean }>) {
  return () => {
    const { tool, running } = handle.props;
    const input = argsValue(tool.args);
    const change = changeLines(tool.args);
    const command = input && typeof input === 'object' && !Array.isArray(input) && typeof (input as { command?: unknown }).command === 'string' ? (input as { command: string }).command : '';
    return (
      <>
        {change && change.length ? (
          <pre aria-label={`Change in ${baseName(String((input as { file_path?: string })?.file_path ?? 'the file'))}`}>{change.map((l) => `${l.kind} ${l.text}`).join('\n')}</pre>
        ) : command ? (
          <pre>{`$ ${command}`}</pre>
        ) : input !== undefined ? (
          <>
            <span class="label">Input</span>
            <pre>{toolInputText(input)}</pre>
          </>
        ) : null}
        {tool.result ? (
          <>
            <span class="label">{tool.isError ? 'Error' : command ? 'Output' : 'Result'}</span>
            <pre data-testid="activity-result">{tool.result}</pre>
          </>
        ) : tool.result === undefined && !running ? (
          <span class="label">No result was reported for this call.</span>
        ) : null}
      </>
    );
  };
}

/** A tool step's line: the verb, what it was about, and how it went. */
function ToolLine(handle: Handle<{ step: ActivityStep; now: number }>) {
  return () => {
    const { step: s, now } = handle.props;
    const verb = stepVerb(s);
    const status = s.state === 'unknown' ? <span class="quiet">no result</span> : null;
    switch (s.kind) {
      case 'read':
        return (
          <>
            <span class="verb">{verb}</span>
            {s.files.slice(0, 4).map((f, i) => (
              <span key={i} class={i === 0 ? 'what' : 'what more'} title={f}>
                {baseName(f)}
              </span>
            ))}
            {s.files.length > 4 ? <span class="quiet">and {s.files.length - 4} more</span> : null}
            {s.state === 'failed' ? <span class="bad">failed</span> : status}
            <At step={s} now={now} />
          </>
        );
      case 'edit':
        return (
          <>
            <span class="verb">{verb}</span>
            <span class="what" title={s.file}>
              {baseName(s.file) || 'a file'}
            </span>
            {s.added || s.removed ? (
              <>
                <span class="add" aria-label={`${s.added} lines added`}>
                  +{s.added}
                </span>
                <span class="del" aria-label={`${s.removed} lines removed`}>
                  −{s.removed}
                </span>
              </>
            ) : null}
            {s.state === 'failed' ? <span class="bad">failed</span> : status}
            <At step={s} now={now} />
          </>
        );
      case 'run':
        return (
          <>
            <span class="verb">{verb}</span>
            <span class="what" title={s.command}>
              {s.command}
            </span>
            {s.failed > 0 ? <span class="bad">{s.failed} failed</span> : s.state === 'failed' ? <span class="bad">failed</span> : status}
            <At step={s} now={now} />
          </>
        );
      case 'search':
        return (
          <>
            <span class="verb">{verb}</span>
            <span class="what" title={s.pattern}>
              {s.pattern}
            </span>
            {s.state === 'failed' ? <span class="bad">failed</span> : status}
            <At step={s} now={now} />
          </>
        );
      case 'tool':
        return (
          <>
            <span class="verb">{verb}</span>
            {s.summary ? (
              <span class="what" title={s.summary}>
                {s.summary}
              </span>
            ) : null}
            {s.state === 'failed' ? <span class="bad">failed</span> : status}
            <At step={s} now={now} />
          </>
        );
      default:
        return null;
    }
  };
}

function StepView(handle: Handle<{ step: ActivityStep; running: boolean; now: number }>) {
  return () => {
    const { step: s, running, now } = handle.props;
    let main;
    if (s.kind === 'text') {
      main = (
        <div class="text" data-testid="activity-text">
          <Markdown source={s.text} />
        </div>
      );
    } else if (s.kind === 'thought') {
      main = (
        <details class="thought" data-testid="activity-reasoning">
          <summary>
            <span>{stepVerb(s)}</span>
            <span class="show">Show</span>
            <At step={s} now={now} />
          </summary>
          <div>{s.text}</div>
        </details>
      );
    } else if (s.kind === 'wait') {
      const open = !s.wait.outcome && running;
      main = (
        <div class="wait" data-open={open ? 'true' : undefined} data-outcome={s.wait.outcome} data-testid="activity-wait">
          <strong>{waitOutcomeText(s.wait.outcome, running)}:</strong>
          <span>{s.wait.title}</span>
          {open ? <span>· answer it under Needs your decision</span> : null}
        </div>
      );
    } else {
      main = (
        <>
          <details class="call">
            <summary class="line">
              <ToolLine step={s} now={now} />
            </summary>
            <div class="body">
              {s.tools.map((tool) => (
                <CallBody key={tool.id} tool={tool} running={running} />
              ))}
            </div>
          </details>
          {s.kind === 'edit' && s.hunk.length ? (
            <div class="hunk" role="group" aria-label={`Change in ${baseName(s.file) || 'the file'}`} data-testid="activity-hunk">
              {s.hunk.map((l, i) => (
                <div key={i} data-kind={l.kind}>
                  {`${l.kind === '-' ? '−' : '+'} ${l.text}`}
                </div>
              ))}
              {s.more > 0 ? <div class="rest">{s.more === 1 ? '1 more line' : `${s.more} more lines`}</div> : null}
            </div>
          ) : null}
          {s.kind === 'run' && s.failing ? (
            <span class="why" data-testid="activity-failing">
              {s.failing}
            </span>
          ) : null}
        </>
      );
    }
    return (
      <div mix={stepCss} data-testid="activity-step" data-kind={s.kind} data-state={s.state}>
        <Tile step={s} />
        <div class="main">{main}</div>
      </div>
    );
  };
}

function EndView(handle: Handle<{ turn: Turn }>) {
  return () => {
    const turn = handle.props.turn;
    const error = turn.status === 'error';
    return (
      <div mix={stepCss}>
        <span class="tile" data-tone={error ? 'danger' : undefined} aria-hidden="true">
          <Icon icon={error ? X : Ban} size={12} strokeWidth={2.2} />
        </span>
        <div class="main">
          {error ? (
            <span class="end" data-status="error" data-testid="activity-error">
              The turn failed: {turn.error}
            </span>
          ) : (
            <span class="end" data-status="cancelled" data-testid="activity-cancelled">
              {turn.stopReason ? 'The turn was interrupted before it finished.' : "The agent's program stopped during the turn."}
            </span>
          )}
        </div>
      </div>
    );
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

/** "Builder · Claude Code WSL · sonnet": the agent of a process, where its CLI ran and its model, as the core recorded them. */
export function agentLine(thread: Pick<AguiThread, 'agentId' | 'adapter' | 'model' | 'runMode' | 'wslDistro'>, agents: readonly Agent[], installs: readonly Connection[] = []): string {
  const name = thread.agentId ? agentName(thread.agentId, agents) : 'An agent';
  const cli = thread.adapter ? cliName(thread.runMode === 'wsl' ? { adapter: thread.adapter, runMode: 'wsl', distro: thread.wslDistro } : { adapter: thread.adapter, runMode: 'native' }, installs) : '';
  return [name, cli, thread.model ?? ''].filter(Boolean).join(' · ');
}

/** The Activity of the task: its head (the agent line of the newest process) and the feed of every process, oldest first. */
export function TaskActivity(handle: Handle<{ taskId: string; agents: readonly Agent[] }>) {
  const s = services(handle);
  const act = query(handle, {
    key: () => handle.props.taskId,
    load: () => s.client.taskActivity(handle.props.taskId),
    when: (e) => e.taskId === handle.props.taskId && e.type.startsWith('agent.'),
    noisy: true,
    debounceMs: 750,
  });
  const installs = query(handle, { load: (): Promise<Connection[]> => s.client.connectionList().catch(() => []), when: (e) => e.type.startsWith('connection.') });
  const list = new VirtualList({ onChange: () => void handle.update(), signal: handle.signal, estimate: 30 });
  const toggled = new Set<string>();
  const toggle = (key: string) => {
    if (toggled.has(key)) toggled.delete(key);
    else toggled.add(key);
    void handle.update();
  };
  // a running step counts its seconds: the feed is drawn again every second while one runs
  let live = false;
  const tick = setInterval(() => {
    if (live) void handle.update();
  }, 1000);
  handle.signal.addEventListener('abort', () => clearInterval(tick));

  const renderRow = (row: ActivityRow, agents: readonly Agent[], newest: string | undefined, now: number) => {
    switch (row.type) {
      case 'thread': {
        const th = row.thread;
        const name = th.agentId ? agentName(th.agentId, agents) : 'An agent';
        const line = agentLine(th, agents, installs.data ?? []);
        return (
          <div mix={[rowCss, threadCss]} data-testid="activity-thread" data-agent={th.agentId} data-thread={th.threadId}>
            {th.agentId ? <Avatar id={th.agentId} name={name} size={22} kind="agent" /> : null}
            <strong>{th.threadId === newest ? name : line}</strong>
            <span class="meta">
              {row.turns === 1 ? '1 turn' : `${row.turns} turns`}
              {row.working ? ' · working now' : ''}
            </span>
          </div>
        );
      }
      case 'plan':
        return (
          <div mix={rowCss} data-thread={row.thread.threadId}>
            <Plan plan={planOf(row.thread.events) ?? []} />
          </div>
        );
      case 'turn':
        return (
          <div mix={rowCss} data-thread={row.thread.threadId}>
            <button type="button" mix={[turnCss, on('click', () => toggle(row.key))]} aria-expanded={row.open ? 'true' : 'false'} data-testid="activity-turn" data-status={row.turn.status}>
              <span class="chev" aria-hidden="true">
                <Icon icon={ChevronRight} size={12} />
              </span>
              <span>
                Turn {row.n} · {turnSummary(row.turn)}
              </span>
              {row.turn.startedAt ? <time class="at">{timeOfDay(row.turn.startedAt)}</time> : null}
            </button>
          </div>
        );
      case 'step':
        return (
          <div mix={rowCss} data-thread={row.thread.threadId}>
            <StepView step={row.step} running={row.turn.status === 'running'} now={now} />
          </div>
        );
      default:
        return (
          <div mix={rowCss} data-thread={row.thread.threadId}>
            <EndView turn={row.turn} />
          </div>
        );
    }
  };

  return () => {
    const { agents } = handle.props;
    const threads = act.data?.threads ?? [];
    const rows = activityRows(threads, toggled);
    live = rows.some((r) => r.type === 'step' && r.step.state === 'running');
    const newest = threads[threads.length - 1];
    const now = Date.now();
    const shown = list.rows(rows, (r) => r.key, (r) => renderRow(r, agents, newest?.threadId, now));
    handle.queueTask(() => list.settle());
    return (
      <section mix={sectionCss} aria-label="Activity" data-testid="task-activity">
        <div class="head">
          <h2>Activity</h2>
          <span class="rule" aria-hidden="true" />
          {newest ? (
            <span class="who" data-testid="activity-agent">
              {agentLine(newest, agents, installs.data ?? [])}
            </span>
          ) : null}
        </div>
        <ErrorNote error={act.error} what="Could not load the activity" />
        {act.data?.truncated ? <p class="note">Only the newest activity is shown.</p> : null}
        {act.data && !threads.length ? <p class="note">No agent has worked on this task yet.</p> : null}
        {rows.length ? (
          <div
            mix={[
              feedCss,
              ref(list.scroller),
              on('scroll', () => list.onScroll()),
            ]}
            role="log"
            aria-label="What the agents did"
            aria-live={list.quiet ? 'off' : 'polite'}
            aria-relevant="additions"
            tabindex={0}
            data-testid="activity-feed"
          >
            {shown}
          </div>
        ) : null}
      </section>
    );
  };
}
