// A run, explained (docs/ui.md S14 and S16): the workflow graph with its checkpoint timeline and a side panel for the
// selected node, then every turn with what it was given (context manifest and exact prompt), what it cost, which model
// and why, plus the checkpoints, MCP calls and events. Its actions (P123): Resume a run that stopped, Time travel from a
// checkpoint of the timeline and Replay an ended run, each after a confirmation that says what runs again; and a
// subworkflow node opens into its own nodes in the graph. Since P124: put the worktree's files back to a safe point (the
// files that change listed first, undoable), also on the way of a time travel, and run an ended task again as a new task.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { ArrowLeft, CopyPlus, Play, Repeat, RotateCcw, Workflow } from 'lucide';
import type { CheckpointView, FileChange, RestorePreview, RestoreResult, RunExplanation } from '../../api/runClient.ts';
import { formatCost, formatDateTime, formatTime, statusLabel } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button, buttonStyle } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Checkbox, formActions } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { Tabs } from '../../ui/tabs.tsx';
import { t } from '../../ui/tokens.ts';
import { NodePanel, NoNodeSelected } from './node-panel.tsx';
import {
  changeWords,
  currentNode,
  expandSubworkflows,
  leaseWords,
  originLine,
  PENDING,
  rerunPlan,
  runAgainLines,
  runCost,
  runTone,
  safePoints,
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
const filesCss = css({
  display: 'grid',
  gap: '2px',
  margin: '0',
  padding: `${t.s2} ${t.s3}`,
  listStyle: 'none',
  maxHeight: '240px',
  overflow: 'auto',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  fontSize: t.textSm,
  '& li': { display: 'flex', gap: t.s2, flexWrap: 'wrap', alignItems: 'baseline' },
  '& code': { fontSize: t.textSm, overflowWrap: 'anywhere' },
});
const pointsCss = css({
  display: 'grid',
  gap: t.s1,
  margin: '0',
  padding: '0',
  border: '0',
  fontSize: t.textMd,
  '& label': { display: 'flex', gap: t.s2, alignItems: 'center', cursor: 'pointer' },
  '& legend': { padding: '0', marginBottom: t.s1, fontWeight: '600' },
});
const dialogBody = css({ display: 'grid', gap: t.s4, '& p': { margin: '0' } });

/** The files a restore changes, each with what happens to it. */
function FileList(handle: Handle<{ changes: FileChange[] }>) {
  return () => {
    const { changes } = handle.props;
    if (!changes.length) return <p mix={muted} data-testid="restore-files">No file changes: the worktree is already as it was then.</p>;
    return (
      <ul mix={filesCss} data-testid="restore-files" aria-label="Files that change">
        {changes.map((c) => (
          <li key={c.path} data-change={c.change}>
            <code>{c.path}</code>
            <span mix={muted}>{changeWords(c)}</span>
          </li>
        ))}
      </ul>
    );
  };
}
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
  /** The worktree's files (P124): previews and restores, apart from the run actions. */
  const fx = action(handle);
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
  /** Time travel's "also put the worktree's files back to this point", and the files that would change (P124). */
  let restoreFiles = false;
  let filesPreview: RestorePreview | undefined;
  /** The restore dialog (P124): the safe point chosen, its preview, the restore just made (and whether it was undone). */
  let restoring: { ref?: string; preview?: RestorePreview; done?: RestoreResult; undone?: boolean } | undefined;
  /** Run again's confirmation (P124). */
  let againAsking = false;

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
    fx.clear();
    asking = { kind, cp };
    restoreFiles = false;
    filesPreview = undefined;
    update();
  };
  const preview = (runId: string, ref: string) => fx.run('preview', () => s.client.runRestorePreview(handle.props.taskId, runId, ref));
  const toggleFiles = async (x: RunExplanation, checked: boolean) => {
    restoreFiles = checked;
    filesPreview = undefined;
    update();
    const ref = asking?.cp?.safePoint;
    if (!checked || !ref || !x.run) return;
    const p = await preview(x.run.id, ref);
    if (p && restoreFiles && asking?.cp?.safePoint === ref) {
      filesPreview = p;
      update();
    }
  };
  const openRestore = () => {
    fx.clear();
    restoring = {};
    update();
  };
  const choosePoint = async (x: RunExplanation, ref: string) => {
    if (!restoring || !x.run) return;
    restoring = { ref };
    update();
    const p = await preview(x.run.id, ref);
    if (p && restoring?.ref === ref && !restoring.done) {
      restoring = { ...restoring, preview: p };
      update();
    }
  };
  const restore = async (x: RunExplanation, ref: string, undo: boolean) => {
    if (!x.run) return;
    const res = await fx.run('restore', () => s.client.runRestore(handle.props.taskId, x.run!.id, ref));
    if (!res) return;
    restoring = { ...restoring, done: res, undone: undo };
    update();
    void q.reload();
  };
  const runAgain = async () => {
    const res = await act.run('again', () => s.client.taskRunAgain(handle.props.taskId));
    if (!res) return;
    againAsking = false;
    update();
    go(routes.task.href({ id: res.taskId }));
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
    const files = a.kind === 'timeTravel' && restoreFiles && !!a.cp?.safePoint;
    const res = await act.run('rerun', () =>
      a.kind === 'resume' ? s.client.runResume(taskId, r.id) : a.kind === 'timeTravel' ? s.client.runTimeTravel(taskId, r.id, a.cp!.id, files) : s.client.runReplay(taskId, r.id, a.cp?.id),
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
    const points = x ? safePoints(x) : [];
    const ended = !!tk && ['COMPLETED', 'FAILED', 'CANCELLED'].includes(tk.status);
    const whys = acts
      ? [
          ...new Set(
            [
              !acts.resume ? acts.resumeWhy : '',
              !acts.replay ? acts.replayWhy : '',
              !acts.timeTravel ? acts.timeTravelWhy : '',
              acts.restore === false && points.length ? acts.restoreWhy : '',
              ended && acts.runAgain === false ? acts.runAgainWhy : '',
            ].filter(Boolean),
          ),
        ]
      : [];
    const plan = x && asking ? rerunPlan(x, asking.kind, asking.cp, asking.kind === 'timeTravel' && restoreFiles ? filesPreview : undefined) : undefined;
    const filesBlocked = !!plan && plan.kind === 'timeTravel' && restoreFiles && (!filesPreview || !filesPreview.allowed);
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
                {acts.restore !== undefined && points.length ? (
                  <Button
                    size="sm"
                    variant="secondary"
                    icon={RotateCcw}
                    disabled={!acts.restore}
                    title={acts.restore ? "Put the worktree's files back to a safe point of this run" : acts.restoreWhy}
                    mix={[on('click', () => openRestore())]}
                  >
                    Put files back...
                  </Button>
                ) : null}
                {ended && acts.runAgain !== undefined ? (
                  <Button
                    size="sm"
                    variant="secondary"
                    icon={CopyPlus}
                    disabled={!acts.runAgain}
                    title={acts.runAgain ? 'A new task with the same work, from its first step' : acts.runAgainWhy}
                    mix={[
                      on('click', () => {
                        act.clear();
                        againAsking = true;
                        update();
                      }),
                    ]}
                  >
                    Run again as a new task
                  </Button>
                ) : null}
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
                  {plan.kind === 'timeTravel' ? (
                    <div style={{ display: 'grid', gap: t.s2 }} data-testid="rerun-files">
                      {asking?.cp?.safePoint ? (
                        <Checkbox
                          label="Also put the worktree's files back to this point"
                          description="To the safe point taken before the next agent turn. What is there now is kept, so this can be undone."
                          checked={restoreFiles}
                          disabled={acts?.restore === false}
                          onChange={(c) => void toggleFiles(x, c)}
                        />
                      ) : (
                        <p mix={muted}>The worktree's files cannot be put back to this point: no agent turn ran after it, so no safe point holds them.</p>
                      )}
                      {asking?.cp?.safePoint && acts?.restore === false ? <p mix={muted}>{acts.restoreWhy}</p> : null}
                      {restoreFiles && fx.busy === 'preview' ? <Loading /> : null}
                      {restoreFiles && filesPreview ? <FileList changes={filesPreview.changes} /> : null}
                      {restoreFiles && filesPreview && !filesPreview.allowed ? <p data-testid="restore-why">{filesPreview.why}</p> : null}
                      <ErrorNote error={fx.error} what="Could not list the files" />
                    </div>
                  ) : null}
                  <ErrorNote error={act.error} what="It did not start" />
                  <div mix={formActions}>
                    <Button variant="primary" disabled={!!act.busy || filesBlocked} mix={[on('click', () => void confirm(x))]}>
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

            <Dialog
              open={!!restoring}
              title="Put the worktree's files back?"
              onClose={() => {
                restoring = undefined;
                update();
              }}
            >
              {restoring ? (
                <div mix={dialogBody} data-testid="restore-dialog">
                  {restoring.done ? (
                    <>
                      <p data-testid="restore-done">
                        {restoring.undone
                          ? `Undone: the files are back as they were before the restore (${restoring.done.changed} ${restoring.done.changed === 1 ? 'file' : 'files'} changed).`
                          : `Put back: ${restoring.done.changed} ${restoring.done.changed === 1 ? 'file' : 'files'} changed. What was there before is kept as `}
                        {restoring.undone ? null : <code>{restoring.done.undoRef}</code>}
                        {restoring.undone ? null : '.'}
                      </p>
                      <ErrorNote error={fx.error} what="It was not undone" />
                      <div mix={formActions}>
                        {restoring.undone ? null : (
                          <Button disabled={!!fx.busy} mix={[on('click', () => void restore(x, restoring!.done!.undoRef, true))]}>
                            {fx.busy === 'restore' ? 'Undoing...' : 'Undo the restore'}
                          </Button>
                        )}
                        <Button
                          variant="primary"
                          mix={[
                            on('click', () => {
                              restoring = undefined;
                              update();
                            }),
                          ]}
                        >
                          Close
                        </Button>
                      </div>
                    </>
                  ) : (
                    <>
                      <p mix={muted}>
                        Drydock takes a safe point of the worktree before every agent turn. The files go back to the one you choose, and what is there now is kept so you
                        can undo it. Commits and the branch are not touched.
                      </p>
                      <fieldset mix={pointsCss}>
                        <legend>Safe point</legend>
                        {points.map((p) => (
                          <label key={p.ref}>
                            <input type="radio" name="safe-point" value={p.ref} checked={restoring!.ref === p.ref} mix={[on('change', () => void choosePoint(x, p.ref))]} />
                            <span>{p.label}</span>
                          </label>
                        ))}
                      </fieldset>
                      {fx.busy === 'preview' ? <Loading /> : null}
                      {restoring.preview ? <FileList changes={restoring.preview.changes} /> : null}
                      {restoring.preview && !restoring.preview.allowed ? <p data-testid="restore-why">{restoring.preview.why}</p> : null}
                      <ErrorNote error={fx.error} what="The files were not put back" />
                      <div mix={formActions}>
                        <Button
                          variant="primary"
                          disabled={!!fx.busy || !restoring.preview?.allowed || !restoring.preview.changes.length}
                          mix={[on('click', () => void restore(x, restoring!.ref!, false))]}
                        >
                          {fx.busy === 'restore' ? 'Putting back...' : 'Put the files back'}
                        </Button>
                        <Button
                          mix={[
                            on('click', () => {
                              restoring = undefined;
                              update();
                            }),
                          ]}
                        >
                          Cancel
                        </Button>
                      </div>
                    </>
                  )}
                </div>
              ) : null}
            </Dialog>

            <Dialog
              open={againAsking && !!tk}
              title={tk ? `Run task #${tk.seq} again?` : ''}
              onClose={() => {
                againAsking = false;
                update();
              }}
            >
              {againAsking && tk ? (
                <div mix={dialogBody} data-testid="run-again-confirm">
                  <ul mix={planCss}>
                    {runAgainLines(tk.seq, tk.status).map((l) => (
                      <li key={l}>{l}</li>
                    ))}
                  </ul>
                  <ErrorNote error={act.error} what="It did not start" />
                  <div mix={formActions}>
                    <Button variant="primary" disabled={!!act.busy} mix={[on('click', () => void runAgain())]}>
                      {act.busy === 'again' ? 'Starting...' : 'Run it again'}
                    </Button>
                    <Button
                      mix={[
                        on('click', () => {
                          againAsking = false;
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
