// A run, explained (docs/ui.md S14 and S16): the workflow graph with its checkpoint timeline and a side panel for the
// selected node, then every turn with what it was given (context manifest and exact prompt), what it cost, which model
// and why, plus the checkpoints, MCP calls and events. Read-only.

import { css, on, ref, type Handle } from 'remix/component';
import { ArrowLeft, Workflow } from 'lucide';
import type { RunExplanation } from '../../api/runClient.ts';
import { formatCost, formatDateTime, formatTime } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { query, services, watch } from '../../state/services.tsx';
import { buttonStyle } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Page } from '../../ui/layout.tsx';
import { Tabs } from '../../ui/tabs.tsx';
import { t } from '../../ui/tokens.ts';
import { NodePanel, NoNodeSelected } from './node-panel.tsx';
import { PENDING, runCost, runTone, statesAsOf } from './run-model.ts';
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
const tabsWrap = css({ display: 'grid', gap: t.s3, minWidth: '0', scrollMarginTop: t.s4 });
const panelCss = css({ paddingTop: t.s3 });

export function RunScreen(handle: Handle<{ taskId: string; runId?: string }>) {
  const s = services(handle);
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
  let selected: string | undefined;
  let openTurn: string | undefined;
  let tabsEl: HTMLElement | undefined;
  let benchEl: HTMLElement | undefined;

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
    const states = x ? statesAsOf(x, cp) : new Map();
    const sel = selected && x?.graph?.nodes.some((n) => n.id === selected) ? selected : undefined;

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
                {r.status}
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
              <div>
                <dt>Status</dt>
                <dd>
                  <Badge tone={runTone(r.status)}>{r.status}</Badge>
                  {r.failedReason ? <span mix={muted}>{r.failedReason}</span> : null}
                </dd>
              </div>
              <div>
                <dt>Started</dt>
                <dd title={formatDateTime(r.created)}>{formatTime(r.created)}</dd>
              </div>
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
                    {i > 0 ? <span aria-hidden="true">· </span> : null}
                    {o.id === r.id ? <strong aria-current="page">{formatTime(o.created)}</strong> : <a href={routes.run.href({ id: taskId, runId: o.id })}>{formatTime(o.created)}</a>} ({o.status})
                  </span>
                ))}
              </p>
            ) : null}

            <div mix={[ref((n) => (benchEl = n as HTMLElement))]} style={{ minWidth: '0', scrollMarginTop: '16px' }}>
              <WorkflowGraph
                x={x}
                agents={agents}
                states={states}
                asOf={cp?.id}
                selected={sel}
                onAsOf={(id) => {
                  asOf = id;
                  update();
                }}
                onSelect={(id) => {
                  selected = id;
                  update();
                }}
                onOpenTurns={() => showTab('turns')}
              >
                {sel ? (
                  <NodePanel
                    key={sel}
                    x={x}
                    nodeId={sel}
                    state={states.get(sel) ?? PENDING}
                    agents={agents}
                    taskId={taskId}
                    asOfStep={cp?.step}
                    onSelect={(id) => {
                      selected = id;
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
          </>
        )}
      </Page>
    );
  };
}
