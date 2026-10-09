// The side panel of the workflow graph: what the selected node is and what it did (status, agent, model, cost), why that
// model (the routing explanation), its exact prompt, every turn it ran and the nodes it connects to. Read-only.

import { css, on, type Handle } from 'remix/component';
import { ArrowLeft, ArrowRight, MousePointerClick, Sparkles, SquareTerminal, X } from 'lucide';
import type { Agent } from '../../api/types.ts';
import type { GraphEdge, RunExplanation } from '../../api/runClient.ts';
import type { NodeState } from '../../lib/graphLayout.ts';
import { agentName, formatCost } from '../../lib/format.ts';
import { services } from '../../state/services.tsx';
import { Button, IconButton } from '../../ui/button.tsx';
import { Avatar, Badge, kv, muted } from '../../ui/data.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { ExplainText } from '../shared/explain.tsx';
import { connections, duration, EXEC_TONE, edgeLabel, execKey, KIND_LABEL, NODE_TONE, statusLine, usageCost } from './run-model.ts';
import { DecisionSummary, PromptView, promptBox } from './turns.tsx';
import { KIND_ICON } from './workflow-graph.tsx';

const panelCss = css({
  display: 'grid',
  alignContent: 'start',
  gap: t.s4,
  padding: t.s4,
  fontSize: t.textMd,
  '& h3': { fontSize: t.textXs, fontWeight: '600', color: t.textFaint, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: t.s2 },
  '& section > p': { margin: '0 0 4px' },
});

const headCss = css({
  display: 'grid',
  gridTemplateColumns: '28px 1fr auto',
  alignItems: 'center',
  gap: `2px ${t.s2}`,
  '& h2': { fontSize: t.textLg, fontWeight: '600', minWidth: '0', overflowWrap: 'anywhere' },
  '& .status': { gridColumn: '2 / 4', display: 'flex', alignItems: 'center', gap: '6px', fontSize: t.textSm, fontWeight: '500' },
  '& .status i': { width: '7px', height: '7px', borderRadius: '50%', display: 'inline-block' },
});

const kindIcon = css({ display: 'grid', placeItems: 'center', width: '28px', height: '28px', borderRadius: t.rMd, background: t.bgActive, color: t.textMuted });

const turnList = css({
  display: 'grid',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  overflow: 'hidden',
  '& li + li': { borderTop: `1px solid ${t.border}` },
  '& button': {
    display: 'flex',
    alignItems: 'center',
    gap: t.s2,
    width: '100%',
    padding: `6px ${t.s3}`,
    border: '0',
    borderRadius: '0',
    background: 'transparent',
    color: t.text,
    fontSize: t.textSm,
    textAlign: 'left',
  },
  '& button:hover': { background: t.bgHover },
  '& .step': { fontFamily: t.mono, color: t.textFaint, minWidth: '48px' },
  '& .dur': { marginLeft: 'auto', color: t.textMuted },
});

const linkList = css({
  display: 'flex',
  flexWrap: 'wrap',
  gap: '6px',
  '& button': {
    display: 'inline-flex',
    alignItems: 'center',
    gap: '4px',
    height: '24px',
    padding: '0 8px',
    border: `1px solid ${t.border}`,
    borderRadius: t.rFull,
    background: t.bgRaised,
    color: t.text,
    fontSize: t.textSm,
  },
  '& button:hover': { borderColor: t.borderStrong },
  '& .when': { color: t.textMuted },
});

const emptyCss = css({
  display: 'grid',
  justifyItems: 'center',
  alignContent: 'center',
  gap: t.s2,
  height: '100%',
  minHeight: '220px',
  padding: t.s6,
  textAlign: 'center',
  color: t.textMuted,
  fontSize: t.textSm,
  '& strong': { color: t.text, fontSize: t.textMd },
  '& .icon': { display: 'grid', placeItems: 'center', width: '40px', height: '40px', borderRadius: t.rLg, background: t.bgActive },
});

/** Shown when no node is selected. */
export function NoNodeSelected() {
  return () => (
    <div mix={emptyCss} data-testid="node-panel-empty">
      <span class="icon">
        <Icon icon={MousePointerClick} size={18} />
      </span>
      <strong>Select a node</strong>
      <p>Click a node, or Tab to the graph, move with the arrow keys and press Enter, to see what it did, why that model and its exact prompt.</p>
    </div>
  );
}

export function NodePanel(
  handle: Handle<{
    x: RunExplanation;
    nodeId: string;
    state: NodeState;
    agents: readonly Agent[];
    taskId: string;
    /** The step of the chosen checkpoint, when one is chosen. */
    asOfStep?: number;
    onSelect: (id: string | undefined) => void;
    onOpenTurn: (key: string) => void;
  }>,
) {
  const s = services(handle);
  let explainFor: string | undefined;
  return () => {
    const { x, nodeId, state: st, agents, taskId, asOfStep, onSelect, onOpenTurn } = handle.props;
    const def = x.graph?.nodes.find((n) => n.id === nodeId);
    const kind = def ? (KIND_LABEL[def.kind] ?? def.kind) : '';
    const last = st.last;
    const color = st.status === 'pending' ? t.textFaint : tone[NODE_TONE[st.status]].fg;
    const turns = x.executions.filter((e) => e.nodeId === nodeId && (asOfStep === undefined || e.step <= asOfStep));
    const cost = turns.reduce((sum, e) => sum + usageCost(e), 0);
    const hasUsage = turns.some((e) => e.usage && e.usage.length);
    const agent = last?.agentId ? agentName(last.agentId, agents) || last.agentId : '';
    const conn = x.graph ? connections(x.graph, nodeId) : { incoming: [], outgoing: [] };
    const d = last?.decision;
    const link = (e: GraphEdge, to: string) => {
      const when = edgeLabel(e);
      return (
        <button key={`${e.from}-${e.to}-${e.kind}`} type="button" mix={[on('click', () => onSelect(to))]} aria-label={`Select ${to}${when ? ` (${when})` : ''}`}>
          {to}
          {when ? <span class="when">{when}</span> : null}
        </button>
      );
    };
    return (
      <div mix={panelCss} data-testid="node-panel" data-node={nodeId}>
        <div mix={headCss}>
          <span mix={kindIcon} aria-hidden="true">
            <Icon icon={(def && KIND_ICON[def.kind]) || Sparkles} size={15} />
          </span>
          <h2>{nodeId}</h2>
          <IconButton icon={X} label="Close the node details" mix={[on('click', () => onSelect(undefined))]} />
          <span class="status" style={{ color }}>
            <i aria-hidden="true" style={{ background: color }} />
            {statusLine(st)}
          </span>
        </div>

        <dl mix={kv}>
          <dt>Kind</dt>
          <dd>
            <Badge>{kind || 'unknown'}</Badge>
          </dd>
          {agent ? (
            <>
              <dt>Agent</dt>
              <dd style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                <Avatar id={last!.agentId!} name={agent} size={18} />
                {agent}
              </dd>
            </>
          ) : null}
          {d?.model ? (
            <>
              <dt>Model</dt>
              <dd>
                <code>{d.model}</code>
              </dd>
            </>
          ) : null}
          {hasUsage ? (
            <>
              <dt>Cost</dt>
              <dd>{formatCost(cost)}</dd>
            </>
          ) : null}
          {last ? (
            <>
              <dt>Last turn</dt>
              <dd>
                step {last.step}
                {last.attempt > 1 ? `, attempt ${last.attempt}` : ''} · {duration(last.started, last.finished)}
              </dd>
            </>
          ) : null}
          {def?.maxAttempts ? (
            <>
              <dt>Max attempts</dt>
              <dd>{def.maxAttempts}</dd>
            </>
          ) : null}
          {def?.fanIn?.length ? (
            <>
              <dt>Waits for</dt>
              <dd>{def.fanIn.join(', ')}</dd>
            </>
          ) : null}
          {def?.request ? (
            <>
              <dt>Asks</dt>
              <dd>{def.request}</dd>
            </>
          ) : null}
        </dl>

        {!last ? <p mix={muted}>{st.status === 'waiting' ? 'This node is waiting for you.' : asOfStep !== undefined ? 'This node had not run at this checkpoint.' : 'This node has not run yet.'}</p> : null}

        {last?.error ? (
          <p>
            <Badge tone="err">error</Badge> {last.error}
          </p>
        ) : null}

        {d ? (
          <section aria-label="Why this model">
            <h3>Why this model</h3>
            <DecisionSummary d={d} />
            <Button
              variant="link"
              size="sm"
              aria-expanded={explainFor === d.id ? 'true' : 'false'}
              mix={[
                on('click', () => {
                  explainFor = explainFor === d.id ? undefined : d.id;
                  void handle.update();
                }),
              ]}
            >
              Explain why this model
            </Button>
            {explainFor === d.id ? (
              <div style={{ marginTop: '8px' }}>
                <ExplainText key={d.id} decisionId={d.id} />
              </div>
            ) : null}
          </section>
        ) : null}

        {last?.hasPrompt && x.run ? (
          <section aria-label="Prompt">
            <h3>Prompt</h3>
            <div mix={promptBox}>
              <PromptView key={execKey(last)} taskId={taskId} runId={x.run.id} exec={last} testId="node-prompt-text" />
            </div>
          </section>
        ) : null}

        {last?.logId ? (
          <div>
            <Button size="sm" variant="ghost" icon={SquareTerminal} mix={[on('click', () => s.ui.openDock('logs'))]}>
              Open its stream in the Logs tab
            </Button>
          </div>
        ) : null}

        {turns.length ? (
          <section aria-label="Turns of this node">
            <h3>Turns ({turns.length})</h3>
            <ul mix={turnList}>
              {turns.map((e) => (
                <li key={execKey(e)}>
                  <button type="button" mix={[on('click', () => onOpenTurn(execKey(e)))]} aria-label={`Open the turn at step ${e.step}${e.attempt > 1 ? `, attempt ${e.attempt}` : ''}`}>
                    <span class="step">step {e.step}</span>
                    {e.attempt > 1 ? <span mix={muted}>attempt {e.attempt}</span> : null}
                    <Badge tone={EXEC_TONE[e.status] ?? 'muted'}>{e.status}</Badge>
                    <span class="dur">{duration(e.started, e.finished)}</span>
                  </button>
                </li>
              ))}
            </ul>
          </section>
        ) : null}

        {conn.incoming.length || conn.outgoing.length ? (
          <section aria-label="Connections">
            <h3>Connections</h3>
            {conn.incoming.length ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '6px' }}>
                <span mix={muted} style={{ display: 'inline-flex' }} title="Comes from">
                  <Icon icon={ArrowLeft} size={14} label="Comes from" />
                </span>
                <div mix={linkList}>{conn.incoming.map((e) => link(e, e.from))}</div>
              </div>
            ) : null}
            {conn.outgoing.length ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                <span mix={muted} style={{ display: 'inline-flex' }} title="Leads to">
                  <Icon icon={ArrowRight} size={14} label="Leads to" />
                </span>
                <div mix={linkList}>{conn.outgoing.map((e) => link(e, e.to))}</div>
              </div>
            ) : null}
          </section>
        ) : null}
      </div>
    );
  };
}
