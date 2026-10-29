// A run, explained (docs/ui.md S14 and S16): the workflow graph with its checkpoint timeline and a side panel for the
// selected node, then every turn with what it was given (context manifest and exact prompt), what it cost, which model
// and why, plus the checkpoints, MCP calls and events. Its actions (P123): Resume a run that stopped, Time travel from a
// checkpoint of the timeline and Replay an ended run, each after a confirmation that says what runs again; and a
// subworkflow node opens into its own nodes in the graph.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { ArrowLeft, Play, Repeat, Workflow } from 'lucide';
import type { CheckpointView, RunExplanation } from '../../api/runClient.ts';
import { formatCost, formatDateTime, formatTime, statusLabel } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { Tabs } from '../../ui/tabs.tsx';
import { t } from '../../ui/tokens.ts';
import { NodePanel, NoNodeSelected } from './node-panel.tsx';
import {
  currentNode,
  expandSubworkflows,
  leaseWords,
  originLine,
  PENDING,
  rerunPlan,
  runCost,
  runTone,
  statesAsOf,
  subExecutions,
  type Cluster,
  type RerunKind,
} from './run-model.ts';
import { Checkpoints, Events, McpCalls } from './run-tables.tsx';
import { Turns } from './turns.tsx';
import { WorkflowGraph } from './workflow-graph.tsx';

type Tab = 'turns' | 'checkpoints' | 'mcp' | 'events';

const statsCss = css({
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))',
  margin: '0',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  '& > div': { display: 'grid', gap: '4px', padding: `${t.s3} ${t.s4}`, borderLeft: `1px solid ${t.border}`, minWidth: '0', marginLeft: '-1px' },
  '& dt': { fontSize: t.textXs, fontWeight: '600', color: t.textFaint, textTransform: 'uppercase', letterSpacing: '0.05em' },
  '& dd': { margin: '0', display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: '6px', minWidth: '0', fontSize: t.textMd, overflowWrap: 'anywhere' },
});

const runsCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: `4px ${t.s2}`,
  margin: '0',
  fontSize: t.textSm,
  color: t.textMuted,
  '& a, & strong': { fontVariantNumeric: 'tabular-nums' },
});

const subCss = css({ display: 'inline-flex', alignItems: 'center', gap: t.s2, '& code': { fontSize: t.textSm } });
const actionsCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: `${t.s2} ${t.s3}`,
  margin: '0',
  fontSize: t.textSm,
  '& p': { margin: '0', color: t.textMuted, flexBasis: '100%' },
});
const planCss = css({ display: 'grid', gap: t.s2, margin: '0', paddingLeft: t.s5, fontSize: t.textMd, lineHeight: '1.5' });
const tabsWrap = css({ display: 'grid', gap: t.s3, minWidth: '0', scrollMarginTop: t.s4 });
const panelCss = css({ paddingTop: t.s3 });

/** The expanded view of a run: its graph with the open subworkflows' own nodes, and their executions beside the run's. */
interface View {
  x: RunExplanation;
  key: string;
  xv: RunExplanation;
  clusters: Cluster[];
}

export function RunScreen(handle: Handle<{ taskId: string; runId?: string; onNavigate?: (href: string) => void }>) {
  const s = services(handle);
  const act = action(handle);
  watch(handle, s.project);
  const task = query(handle, {
    load: () => s.client.taskGet(handle.props.taskId),
    key: () => handle.props.taskId,
    when: (e) => e.taskId === handle.props.taskId && e.type.startsWith('task.'),
  });
  const q = query(handle, {
    load: () => s.client.runExplain(handle.props.taskId, handle.props.runId),
    key: () => [handle.props.taskId, handle.props.runId ?? ''],
    when: (e) => e.taskId === handle.props.taskId || (!!e.runId && e.runId === handle.props.runId),
    debounceMs: 400,
  });

  let tab: Tab = 'turns';
  let asOf: string | undefined;
  /** The node shown in the side panel; undefined: the node the run is at (UI review P1-7), null: none (the person closed it). */
  let selected: string | null | undefined;
  let openTurn: string | undefined;
  let tabsEl: HTMLElement | undefined;
  let benchEl: HTMLElement | undefined;
  /** The subworkflow nodes whose own nodes the graph shows. */
  const open = new Set<string>();
  let view: View | undefined;
  /** The action waiting for its confirmation. */
  let asking: { kind: RerunKind; cp?: CheckpointView } | undefined;

  const viewOf = (x: RunExplanation): View => {
    const key = [...open].sort().join('|');
    if (view?.x !== x || view.key !== key) {
      const ex = x.graph && open.size ? expandSubworkflows(x.graph, open) : undefined;
      view = { x, key, xv: ex ? { ...x, graph: ex.graph, executions: [...x.executions, ...subExecutions(x, open)] } : x, clusters: ex?.clusters ?? [] };
    }
    return view;
  };
  const toggleSub = (id: string) => {
    if (open.has(id)) {
      for (const o of [...open]) if (o === id || o.startsWith(`${id}/`)) open.delete(o);
    } else open.add(id);
    update();
  };
  const ask = (kind: RerunKind, cp?: CheckpointView) => {
    act.clear();
    asking = { kind, cp };
    update();
  };
  const go = (href: string) => {
    if (handle.props.onNavigate) handle.props.onNavigate(href);
    else void navigate(href);
  };
  const confirm = async (x: RunExplanation) => {
    const r = x.run;
    const a = asking;
    if (!r || !a) return;
    const { taskId } = handle.props;
    const res = await act.run('rerun', () =>
      a.kind === 'resume' ? s.client.runResume(taskId, r.id) : a.kind === 'timeTravel' ? s.client.runTimeTravel(taskId, r.id, a.cp!.id) : s.client.runReplay(taskId, r.id, a.cp?.id),
    );
    if (!res) return;
    asking = undefined;
    asOf = undefined;
    update();
    if (res.runId && res.runId !== r.id) go(routes.run.href({ id: taskId, runId: res.runId }));
    else void q.reload();
  };

  const update = () => void handle.update();
  const showTab = (name: Tab, scrollTo?: string) => {
    tab = name;
    update();
    handle.queueTask(() => {
      const target = scrollTo ? tabsEl?.querySelector(`[data-key="${CSS.escape(scrollTo)}"]`) : tabsEl;
      target?.scrollIntoView?.({ block: scrollTo ? 'center' : 'start', behavior: 'smooth' });
    });
  };

  return () => {
    const { taskId } = handle.props;
    const x: RunExplanation | undefined = q.data;
    const tk = task.data?.task;
    const title = tk ? `Run of #${tk.seq} ${tk.title}` : 'Run';
    const agents = s.project.agentList;
    const r = x?.run;
    const cp = x?.checkpoints.find((c) => c.id === asOf);
    const v = x ? viewOf(x) : undefined;
    const xv = v?.xv;
    const states = xv ? statesAsOf(xv, cp) : new Map();
    const at = xv?.graph ? currentNode(xv.graph, states) : undefined;
    const want = selected === null ? undefined : (selected ?? at);
    const sel = want && xv?.graph?.nodes.some((n) => n.id === want) ? want : undefined;
    const acts = x?.actions;
    const whys = acts ? [...new Set([!acts.resume ? acts.resumeWhy : '', !acts.replay ? acts.replayWhy : '', !acts.timeTravel ? acts.timeTravelWhy : ''].filter(Boolean))] : [];
    const plan = x && asking ? rerunPlan(x, asking.kind, asking.cp) : undefined;
    const origin = x ? originLine(x) : undefined;
    const lease = x ? leaseWords(x) : undefined;

    return (
      <Page
        title={title}
        icon={Workflow}
        wide
        testId="run-screen"
        subtitle={
          r ? (
            <span mix={subCss}>
              <code data-testid="run-id">{r.id}</code>
            </span>
          ) : undefined
        }
        actions={
          <>
            {r ? (
              <Badge tone={runTone(r.status)} testId="run-status">
                {statusLabel(r.status)}
              </Badge>
            ) : null}
            <a href={routes.task.href({ id: taskId })} mix={buttonStyle('ghost', 'sm')}>
              <Icon icon={ArrowLeft} size={14} />
              Back to the task
            </a>
          </>
        }
      >
        <ErrorNote error={q.error} what="Could not explain the run" />
        {!x ? (
          q.loading || !q.error ? <Loading /> : null
        ) : !r ? (
          <Empty title="This task has not run yet." icon={Workflow}>
            <p>When the task starts, its workflow appears here node by node, with every turn explained.</p>
          </Empty>
        ) : (
          <>
            <dl mix={statsCss} aria-label="Run summary">
              <div>
                <dt>Workflow</dt>
                <dd>
                  <code>{r.workflow}</code>
                </dd>
              </div>
              {r.failedReason ? (
                <div>
                  <dt>Why it failed</dt>
                  <dd>
                    <span mix={muted}>{r.failedReason}</span>
                  </dd>
                </div>
              ) : null}
              <div>
                <dt>Started</dt>
                <dd title={formatDateTime(r.created)}>{formatTime(r.created)}</dd>
              </div>
              {origin && x.origin ? (
                <div data-testid="run-origin">
                  <dt>Started from</dt>
                  <dd>
                    <a href={routes.run.href({ id: taskId, runId: x.origin.run })}>{origin}</a>
                  </dd>
                </div>
              ) : null}
              {lease ? (
                <div data-testid="run-lease">
                  <dt>Driven by</dt>
                  <dd>{lease}</dd>
                </div>
              ) : null}
              <div>
                <dt>Cost of its turns</dt>
                <dd>{formatCost(runCost(x))}</dd>
              </div>
              <div>
                <dt>Turns</dt>
                <dd>{x.executions.length}</dd>
              </div>
            </dl>

            {x.runs.length > 1 ? (
              <p mix={runsCss} data-testid="run-history">
                <span>This task ran {x.runs.length} times:</span>
                {x.runs.map((o, i) => (
                  <span key={o.id}>
                    {i > 0 ? (
                      <span aria-hidden="true" style={{ marginRight: '8px', color: t.textFaint }}>
                        ·
                      </span>
                    ) : null}
                    {o.id === r.id ? <strong aria-current="page">{formatTime(o.created)}</strong> : <a href={routes.run.href({ id: taskId, runId: o.id })}>{formatTime(o.created)}</a>} ({o.status})
                  </span>
                ))}
              </p>
            ) : null}

            {acts ? (
              <div mix={actionsCss} role="group" aria-label="Run actions" data-testid="run-actions">
                <Button size="sm" variant="secondary" icon={Play} disabled={!acts.resume} title={acts.resume ? 'Go on from the latest checkpoint' : acts.resumeWhy} mix={[on('click', () => ask('resume'))]}>
                  Resume
                </Button>
                <Button size="sm" variant="secondary" icon={Repeat} disabled={!acts.replay} title={acts.replay ? 'Run this run again from its start' : acts.replayWhy} mix={[on('click', () => ask('replay'))]}>
                  Replay from the start
                </Button>
                {acts.timeTravel && x.graph ? <span mix={muted}>To time travel, choose a checkpoint under the graph.</span> : null}
                {whys.length ? <p data-testid="run-actions-why">{whys.join(' ')}</p> : null}
              </div>
            ) : null}

            <div mix={[ref((n) => (benchEl = n as HTMLElement))]} style={{ minWidth: '0', scrollMarginTop: '16px' }}>
              <WorkflowGraph
                x={xv!}
                graph={xv!.graph}
                clusters={v!.clusters}
                onRerun={acts ? (kind, c) => ask(kind, c) : undefined}
                agents={agents}
                states={states}
                asOf={cp?.id}
                selected={sel}
                current={at}
                onAsOf={(id) => {
                  asOf = id;
                  update();
                }}
                onSelect={(id) => {
                  selected = id ?? null;
                  update();
                }}
                onOpenTurns={() => showTab('turns')}
              >
                {sel ? (
                  <NodePanel
                    key={sel}
                    x={xv!}
                    source={x}
                    expanded={open.has(sel)}
                    onToggleSub={toggleSub}
                    nodeId={sel}
                    state={states.get(sel) ?? PENDING}
                    agents={agents}
                    taskId={taskId}
                    asOfStep={cp?.step}
                    onSelect={(id) => {
                      selected = id ?? null;
                      update();
                    }}
                    onOpenTurn={(key) => {
                      openTurn = key;
                      showTab('turns', key);
                    }}
                  />
                ) : (
                  <NoNodeSelected />
                )}
              </WorkflowGraph>
            </div>

            <div mix={[tabsWrap, ref((n) => (tabsEl = n as HTMLElement))]}>
              <Tabs
                label="Run"
                active={tab}
                onChange={(name) => {
                  tab = name as Tab;
                  update();
                }}
                items={[
                  { name: 'turns', label: 'Turns', count: x.executions.length },
                  { name: 'checkpoints', label: 'Checkpoints', count: x.checkpoints.length },
                  { name: 'mcp', label: 'MCP calls', count: x.mcpCalls.length },
                  { name: 'events', label: 'Events', count: x.events.length },
                ]}
                panelMix={panelCss}
              >
                {(name) =>
                  name === 'turns' ? (
                    <Turns
                      x={x}
                      taskId={taskId}
                      agents={agents}
                      open={openTurn}
                      highlight={sel}
                      onToggle={(key) => {
                        openTurn = key;
                        update();
                      }}
                    />
                  ) : name === 'checkpoints' ? (
                    <Checkpoints
                      x={x}
                      asOf={cp?.id}
                      onShow={x.graph ? (id) => {
                        asOf = id;
                        update();
                        handle.queueTask(() => benchEl?.scrollIntoView?.({ block: 'start', behavior: 'smooth' }));
                      } : undefined}
                    />
                  ) : name === 'mcp' ? (
                    <McpCalls x={x} agents={agents} />
                  ) : (
                    <Events x={x} agents={agents} />
                  )
                }
              </Tabs>
            </div>

            <Dialog
              open={!!plan}
              title={plan?.title ?? ''}
              onClose={() => {
                asking = undefined;
                update();
              }}
            >
              {plan ? (
                <div style={{ display: 'grid', gap: t.s4 }} data-testid="rerun-confirm" data-kind={plan.kind}>
                  <ul mix={planCss} data-testid="rerun-plan">
                    {plan.lines.map((l) => (
                      <li key={l}>{l}</li>
                    ))}
                  </ul>
                  <ErrorNote error={act.error} what="It did not start" />
                  <div mix={formActions}>
                    <Button variant="primary" disabled={!!act.busy} mix={[on('click', () => void confirm(x))]}>
                      {act.busy ? 'Starting...' : plan.confirm}
                    </Button>
                    <Button
                      mix={[
                        on('click', () => {
                          asking = undefined;
                          update();
                        }),
                      ]}
                    >
                      Cancel
                    </Button>
                  </div>
                </div>
              ) : null}
            </Dialog>
          </>
        )}
      </Page>
    );
  };
}
