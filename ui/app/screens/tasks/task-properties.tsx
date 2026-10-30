// The task page's properties panel (the graphite mockup "Task", decision P97): Status, Agent, Reviewer, Runs on, Model, Routing
// (the task's routing level, P127), Branch, Template and Labels (editable), the links, the step the task is at, the Checks grid, "This run" (elapsed, test runs, cost so
// far), and the buttons: Open the worktree (O: a terminal in the dock, in the task's worktree) and Hand to another agent (the
// project's agents; the core picks the reviewer and its refusal is shown). Its class task-meta is what the end-to-end suite
// compares before and after a reload.

import { css, on, type Handle } from 'remix/component';
import { Check, FolderOpen, Pencil, ScrollText, Workflow, X } from 'lucide';
import type { AssignPreview } from '../../api/controlClient.ts';
import type { Agent, Connection, PresenceState, SummaryCheck, Task, TaskDetail, UsageRow } from '../../api/types.ts';
import { agentName, formatCost, formatDateTime, presenceFor, statusLabel } from '../../lib/format.ts';
import { ownsKeys } from '../../lib/keys.ts';
import { routes } from '../../routes.ts';
import { action, services } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Avatar, Badge, SourceBadge, faint, presenceColor } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { errorAttrs } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Menu } from '../../ui/menu.tsx';
import { t } from '../../ui/tokens.ts';
import { cliName } from '../shared/cli-name.ts';
import { SourceTag, distinctSources } from '../shared/usage.tsx';
import { clockText } from './activity-steps.ts';
import { stepsFor, type Step } from './steps.ts';
import { CancelTask } from './task-controls.tsx';
import { TaskRoutingLevel } from './task-level.tsx';
import { addLabel, CHECK_WORDS, checkMark, checkName, runMs, runNumber, statusDot, statusWords, testRuns } from './task-page.ts';
import { isRunActive, isTerminal } from './tasks.ts';

const panelCss = css({
  display: 'flex',
  flexDirection: 'column',
  gap: '22px',
  minWidth: '0',
  fontSize: t.textMd,
  '& h2': { margin: '0', fontSize: t.textXs, fontWeight: '500', color: t.textFaint },
  '& dl': { display: 'grid', gridTemplateColumns: '96px minmax(0, 1fr)', gap: `12px 10px`, margin: '0', alignItems: 'center' },
  '& dt': { color: t.textFaint },
  '& dd': { margin: '0', minWidth: '0', display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2, overflowWrap: 'anywhere' },
  '& dd code, & dd .mono': { minWidth: '0', padding: '0', background: 'transparent', fontFamily: t.mono, fontSize: t.textXs, color: t.textMuted, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', display: 'block' },
  '& .group': { display: 'grid', gap: '10px', minWidth: '0' },
  '& .group > .head': { display: 'flex', alignItems: 'baseline', gap: t.s2 },
  '& .group > .head > .aside': { marginLeft: 'auto', color: t.textFaint, fontSize: t.textXs },
  '& .foot': { margin: '0', color: t.textFaint, fontSize: t.textXs, overflowWrap: 'anywhere' },
});

const dotCss = css({ display: 'inline-block', width: '8px', height: '8px', borderRadius: '50%', flex: 'none' });

const chipCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  height: '22px',
  padding: '0 8px',
  borderRadius: t.rSm,
  background: t.bgActive,
  color: t.textMuted,
  fontSize: t.textXs,
});

const labelsEditCss = css({
  display: 'grid',
  gap: t.s2,
  width: '100%',
  '& form': { display: 'flex', gap: t.s2, alignItems: 'center' },
  '& input': { flex: '1', minWidth: '0', height: '28px' },
  '& ul': { display: 'flex', flexWrap: 'wrap', gap: '6px', margin: '0', padding: '0', listStyle: 'none' },
  '& li': { display: 'inline-flex', alignItems: 'center', gap: '2px', paddingLeft: '8px', borderRadius: t.rSm, background: t.bgActive, color: t.textMuted, fontSize: t.textXs },
  '& li button': { width: '28px', minHeight: '28px', height: '28px' },
  '& .err': { margin: '0', color: t.danger, fontSize: t.textSm },
});

// the step the task is at: one row per step, a dot and the step's note
const stepsCss = css({
  display: 'grid',
  gap: '6px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0', color: t.textMuted },
  '& > li > .d': { width: '7px', height: '7px', borderRadius: '50%', flex: 'none', background: t.borderStrong },
  '& > li[data-state="done"] > .d': { background: t.success },
  '& > li[data-state="current"] > .d': { background: t.accent, boxShadow: `0 0 0 2px ${t.accentSoft}` },
  '& > li[data-state="failed"] > .d': { background: t.danger },
  '& > li[data-state="current"]': { color: t.text },
  '& > li > small': { marginLeft: 'auto', color: t.textFaint, fontSize: t.textXs, minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
});

const checksCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
  gap: '6px 12px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': { display: 'flex', alignItems: 'center', gap: t.s2, minWidth: '0' },
  '& > li > .d': { width: '7px', height: '7px', borderRadius: '50%', flex: 'none', background: t.borderStrong },
  '& > li[data-state="passed"] > .d': { background: t.success },
  '& > li[data-state="failed"] > .d': { background: t.danger },
  '& > li[data-state="running"] > .d': { background: t.accent, boxShadow: `0 0 0 2px ${t.accentSoft}` },
  '& > li[data-state="inconclusive"] > .d': { background: t.warning },
});

const tilesCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(3, minmax(0, 1fr))',
  gap: t.s2,
  '& > div': { display: 'flex', flexDirection: 'column', gap: '2px', minWidth: '0', padding: '10px', border: `1px solid ${t.border}`, borderRadius: t.rLg },
  '& .v': { fontFamily: t.mono, fontSize: t.textLg, color: t.text, fontVariantNumeric: 'tabular-nums', overflowWrap: 'anywhere' },
  '& .v[data-none="true"]': { fontFamily: t.font, fontSize: t.textXs, color: t.textFaint, lineHeight: '22px' },
  '& .k': { color: t.textFaint, fontSize: t.textXs },
  '& .src': { display: 'flex', flexWrap: 'wrap', gap: '4px' },
});

const actionsCss = css({
  marginTop: 'auto',
  display: 'grid',
  gap: t.s2,
  '& > .links': { display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2 },
});

const WIDE = { height: '34px', width: '100%', justifyContent: 'center' };

const DOT_TONE = { accent: t.accent, warn: t.warning, ok: t.success, err: t.danger, muted: t.textFaint } as const;

/** A person or agent with its dot, or "unassigned". */
function Person(handle: Handle<{ id: string | undefined; agents: readonly Agent[]; presence: readonly PresenceState[] | undefined; you?: string }>) {
  return () => {
    const { id, agents, presence, you } = handle.props;
    if (!id) return <span mix={faint}>unassigned</span>;
    if (id.startsWith('human:')) {
      const name = id.slice(6);
      return (
        <>
          <Avatar id={id} name={name} size={18} kind="human" />
          <span>{you && id === you ? `${name} (you)` : name}</span>
        </>
      );
    }
    const name = agentName(id, agents);
    return (
      <>
        <Avatar id={id} name={name} size={18} kind="agent" status={presenceColor(presenceFor(id, presence))} />
        <span>{name}</span>
      </>
    );
  };
}

/** Where the assignee's CLI runs, in the Connections page's words ("Claude Code WSL"), from the core's own resolution. */
export function runsOn(task: Pick<Task, 'assignee'>, preview: AssignPreview | undefined, agents: readonly Agent[], installs: readonly Connection[]): string {
  if (!task.assignee) return '';
  if (task.assignee.startsWith('human:')) return 'You, by hand';
  const a = preview?.agent;
  if (a && a.id === task.assignee) return cliName({ adapter: a.adapter, runMode: a.runMode, distro: a.wslDistro }, installs);
  const ag = agents.find((x) => x.id === task.assignee);
  if (!ag) return '';
  return ag.runMode === 'wsl' ? cliName({ adapter: ag.adapter, runMode: 'wsl', distro: ag.wslDistro }, installs) : cliName({ adapter: ag.adapter, runMode: 'native' }, installs);
}

/** The compact step list: Spec, Code, Checks, Review, You, each with its state and its note. */
function StepList(handle: Handle<{ steps: Step[] }>) {
  return () => (
    <ol mix={stepsCss} aria-label="Progress" data-testid="task-steps">
      {handle.props.steps.map((st) => (
        <li key={st.name} data-step={st.name} data-state={st.state} aria-current={st.state === 'current' ? 'step' : undefined}>
          <span class="d" aria-hidden="true" />
          <span>{st.label}</span>
          <small>
            {st.note || (st.state === 'done' ? 'done' : st.state === 'current' ? 'now' : st.state === 'failed' ? 'failed' : 'to do')}
            <span class="sr-only">{st.note ? `, ${st.state === 'todo' ? 'to do' : st.state}` : ''}</span>
          </small>
        </li>
      ))}
    </ol>
  );
}

/** The labels as chips; Edit labels opens a box to add one (Enter) and a remove button on each. */
function Labels(handle: Handle<{ task: Task; onChanged: (t: Task) => void }>) {
  const s = services(handle);
  const act = action(handle);
  let editing = false;
  let typed = '';
  let error = '';
  const save = async (labels: string[]) => {
    const tk = await act.run('labels', () => s.client.taskLabelsSet(handle.props.task.id, labels));
    if (tk) {
      typed = '';
      handle.props.onChanged(tk);
    }
  };
  const add = () => {
    const r = addLabel(handle.props.task.labels ?? [], typed);
    if ('error' in r) {
      error = r.error;
      void handle.update();
      handle.queueTask(() => document.getElementById(`labels-${handle.id}`)?.focus());
      return;
    }
    error = '';
    void save(r.labels);
  };
  return () => {
    const { task } = handle.props;
    const labels = task.labels ?? [];
    const inputId = `labels-${handle.id}`;
    const errId = `${inputId}-error`;
    if (!editing) {
      return (
        <>
          {labels.map((l) => (
            <span key={l} mix={chipCss} data-testid="task-label">
              {l}
            </span>
          ))}
          {labels.length ? null : <span mix={faint}>none</span>}
          <IconButton
            icon={Pencil}
            size={13}
            label="Edit labels"
            mix={on('click', () => {
              editing = true;
              void handle.update();
              handle.queueTask(() => document.getElementById(inputId)?.focus());
            })}
          />
        </>
      );
    }
    return (
      <div mix={labelsEditCss} data-testid="labels-edit">
        {labels.length ? (
          <ul aria-label="Labels">
            {labels.map((l) => (
              <li key={l}>
                {l}
                <IconButton icon={X} size={12} label={`Remove label ${l}`} disabled={act.busy !== ''} mix={on('click', () => void save(labels.filter((x) => x !== l)))} />
              </li>
            ))}
          </ul>
        ) : null}
        <form
          mix={on('submit', (e) => {
            e.preventDefault();
            add();
          })}
        >
          <label for={inputId} class="sr-only">
            Add a label
          </label>
          <input
            id={inputId}
            type="text"
            value={typed}
            placeholder="Add a label"
            autocomplete="off"
            {...errorAttrs(errId, error || act.error?.message)}
            mix={on('input', (e) => {
              typed = e.currentTarget.value;
              error = '';
              void handle.update();
            })}
          />
          <IconButton
            icon={Check}
            size={14}
            label="Done with labels"
            mix={on('click', () => {
              editing = false;
              error = '';
              typed = '';
              act.clear();
              void handle.update();
            })}
          />
        </form>
        {error || act.error ? (
          <p class="err" id={errId} role="alert">
            {error || act.error?.message}
          </p>
        ) : null}
      </div>
    );
  };
}

/** Source badges for the task's cost, taken from the usage rows the core returns (reported, estimated, or both); none without a cost. */
function CostSources(handle: Handle<{ rows: readonly UsageRow[]; costUsd: number }>) {
  return () => {
    const { rows, costUsd } = handle.props;
    if (!(costUsd > 0)) return null;
    if (rows.length === 0) return <SourceBadge source="unknown" />;
    return (
      <>
        {distinctSources(rows).map((src) => (
          <SourceTag key={src} source={src} />
        ))}
      </>
    );
  };
}

export interface PropertiesProps {
  detail: TaskDetail;
  agents: readonly Agent[];
  presence: readonly PresenceState[] | undefined;
  usage: readonly UsageRow[];
  tasks: readonly Task[];
  checks: readonly SummaryCheck[];
  preview: AssignPreview | undefined;
  installs: readonly Connection[];
  /** app.info's human ("human:kyle"). */
  you?: string;
  onChanged: () => void;
  onTask: (t: Task) => void;
}

export function TaskProperties(handle: Handle<PropertiesProps>) {
  const s = services(handle);
  const act = action(handle);
  const openWorktree = () => {
    const task = handle.props.detail.task;
    if (!task.worktree) return;
    s.ui.openTerminal({ taskId: task.id });
  };
  const hand = async (agentId: string) => {
    const r = await act.run('hand', () => s.client.taskAssign(handle.props.detail.task.id, agentId));
    if (r) handle.props.onChanged();
  };
  // O opens the worktree, from anywhere on the page but a text field or a widget that owns its keys
  window.addEventListener(
    'keydown',
    (e) => {
      if (e.key !== 'o' && e.key !== 'O') return;
      if (e.ctrlKey || e.metaKey || e.altKey || e.repeat || e.defaultPrevented || ownsKeys(e.target) || s.ui.paletteOpen) return;
      const el = e.target as HTMLElement | null;
      if (el && (el.isContentEditable || /^(input|textarea|select)$/i.test(el.tagName) || el.closest?.('[role=combobox], [role=listbox], [role=menu], dialog[open]'))) return;
      if (!handle.props.detail.task.worktree) return;
      e.preventDefault();
      openWorktree();
    },
    { signal: handle.signal },
  );
  // the elapsed time counts while the task runs
  const tick = setInterval(() => {
    const task = handle.props.detail.task;
    if (task.started && !task.completed && !isTerminal(task.status)) void handle.update();
  }, 1000);
  handle.signal.addEventListener('abort', () => clearInterval(tick));

  return () => {
    const { detail: d, agents, presence, usage, tasks, checks, preview, installs, you, onChanged, onTask } = handle.props;
    const task = d.task;
    const deps = (task.dependsOn ?? []).map((id) => tasks.find((x) => x.id === id) ?? { id, seq: 0, title: id });
    const writer = agents.find((a) => a.id === task.assignee);
    const model = task.model || writer?.model || '';
    const ms = runMs(task);
    const runs = testRuns(d.evidence);
    const run = runNumber(task);
    const others = agents.filter((a) => a.id !== task.assignee);
    const where = runsOn(task, preview, agents, installs);
    const runActive = isRunActive(d.run);
    return (
      <div mix={panelCss} class="task-meta" role="group" aria-label="Task properties">
        <dl>
          <dt>Status</dt>
          <dd data-testid="task-state">
            <span mix={dotCss} aria-hidden="true" style={{ background: DOT_TONE[statusDot(task, d.paused)] }} />
            {statusWords(task, d.paused, checks)}
          </dd>
          <dt>Agent</dt>
          <dd>
            <Person id={task.assignee} agents={agents} presence={presence} you={you} />
          </dd>
          <dt>Reviewer</dt>
          <dd>
            <Person id={task.reviewer} agents={agents} presence={presence} you={you} />
          </dd>
          {where ? (
            <>
              <dt>Runs on</dt>
              <dd data-testid="task-runs-on">{where}</dd>
            </>
          ) : null}
          <dt>Model</dt>
          <dd>{model ? <span>{task.effort ? `${model} · ${task.effort}` : model}</span> : <span mix={faint}>default</span>}</dd>
          <dt>Routing</dt>
          <dd>
            <TaskRoutingLevel taskId={task.id} />
          </dd>
          {task.branch ? (
            <>
              <dt>Branch</dt>
              <dd>
                <code title={task.branch}>{task.branch}</code>
              </dd>
            </>
          ) : null}
          <dt>Template</dt>
          <dd>{task.template ? <span>{task.template}</span> : <span mix={faint}>default</span>}</dd>
          <dt>Labels</dt>
          <dd data-testid="task-labels">
            <Labels task={task} onChanged={onTask} />
          </dd>
          {task.acceptedBy ? (
            <>
              <dt>Accepted</dt>
              <dd>
                <Badge tone="ok">Accepted by {task.acceptedBy}</Badge>
              </dd>
            </>
          ) : null}
          {task.issueRef ? (
            <>
              <dt>Issue</dt>
              <dd>{task.issueRef}</dd>
            </>
          ) : null}
          {task.prRef ? (
            <>
              <dt>PR</dt>
              <dd>
                <a href={routes.pulls.href()}>{task.prRef}</a>
              </dd>
            </>
          ) : null}
          {deps.length > 0 ? (
            <>
              <dt>Depends on</dt>
              <dd>
                {deps.map((x) => (
                  <a key={x.id} href={routes.task.href({ id: x.id })}>
                    {x.seq ? `#${x.seq} ${x.title}` : x.title}
                  </a>
                ))}
              </dd>
            </>
          ) : null}
          {d.run ? (
            <>
              <dt>Workflow</dt>
              <dd>
                <span>
                  {d.run.workflow} · {statusLabel(d.run.status).toLowerCase()}
                </span>
              </dd>
            </>
          ) : null}
        </dl>

        <div class="group">
          <h2>Steps</h2>
          <StepList steps={stepsFor(task, d.evidence, d.reviews, d.pending)} />
        </div>

        <div class="group">
          <h2>Checks</h2>
          {checks.length ? (
            <ul mix={checksCss} aria-label="Checks" data-testid="task-checks">
              {checks.map((c) => {
                const mark = checkMark(c.status);
                return (
                  <li key={c.kind} data-check={c.kind} data-state={mark} title={c.reason ? `${CHECK_WORDS[mark]}: ${c.reason}` : CHECK_WORDS[mark]}>
                    <span class="d" aria-hidden="true" />
                    <span>{checkName(c.kind)}</span>
                    <span class="sr-only">, {CHECK_WORDS[mark]}</span>
                  </li>
                );
              })}
            </ul>
          ) : (
            <p class="foot">No checks have run yet.</p>
          )}
        </div>

        <div class="group">
          <div class="head">
            <h2>This run</h2>
            {run ? (
              <span class="aside">
                run {run} of {task.maxIterations}
              </span>
            ) : null}
          </div>
          <div mix={tilesCss}>
            <div>
              <span class="v" data-testid="task-elapsed" data-none={ms === undefined ? 'true' : undefined}>
                {ms === undefined ? 'not started' : clockText(ms)}
              </span>
              <span class="k">elapsed</span>
            </div>
            <div>
              <span class="v" data-testid="task-test-runs">
                {runs}
              </span>
              <span class="k">test runs</span>
            </div>
            <div>
              <span class="v" data-testid="task-cost" data-none={task.costUsd > 0 ? undefined : 'true'}>
                {formatCost(task.costUsd)}
              </span>
              <span class="k">so far</span>
              <span class="src">
                <CostSources rows={usage} costUsd={task.costUsd} />
              </span>
            </div>
          </div>
        </div>

        <div mix={actionsCss}>
          <ErrorNote error={act.error} what="Not handed on" />
          <Button
            style={WIDE}
            icon={FolderOpen}
            disabled={!task.worktree}
            title={task.worktree ? `A terminal in ${task.worktree}` : 'The task has no worktree yet: it gets one when it starts'}
            mix={on('click', openWorktree)}
          >
            Open the worktree <kbd>O</kbd>
          </Button>
          {isTerminal(task.status) ? null : (
            <Menu
              label="Hand to another agent"
              placement="bottom-start"
              items={others.length ? others.map((a) => ({ name: a.id, label: `${a.name} (${a.role})`, value: a.id })) : [{ name: '', label: 'No other agent on the team', disabled: true }]}
              onSelect={(id) => {
                if (id) void hand(id);
              }}
              trigger={(mix) => (
                <Button variant="ghost" style={WIDE} disabled={act.busy !== ''} mix={mix}>
                  {act.busy === 'hand' ? 'Handing on...' : 'Hand to another agent'}
                </Button>
              )}
            />
          )}
          <div class="links">
            <Button size="sm" variant="ghost" icon={ScrollText} mix={on('click', () => s.ui.openDock('logs'))}>
              Logs
            </Button>
            {d.run ? (
              <a href={routes.run.href({ id: task.id })} style={{ display: 'inline-flex', alignItems: 'center', gap: '6px', minHeight: '28px', fontSize: t.textSm }}>
                <Icon icon={Workflow} size={14} />
                Workflow and explanation
              </a>
            ) : null}
            {runActive && !isTerminal(task.status) ? <CancelTask taskId={task.id} onChanged={onChanged} /> : null}
          </div>
          <p class="foot">
            Created {formatDateTime(task.created) || 'not recorded'} · updated {formatDateTime(task.updated) || 'not recorded'}
          </p>
        </div>
      </div>
    );
  };
}

