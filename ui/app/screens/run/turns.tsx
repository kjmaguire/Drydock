// Every turn of a run, explained: what it was given (context manifest), the exact prompt, which model and why, what it
// cost. Read-only.

import { css, on, ref, type Handle } from 'remix/component';
import { ChevronRight, ScrollText } from 'lucide';
import type { Agent } from '../../api/types.ts';
import type { ExecView, RoutingDecisionView, RunExplanation } from '../../api/runClient.ts';
import { agentName, formatCost } from '../../lib/format.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Avatar, Badge, mono, muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { tableCard } from '../../ui/layout.tsx';
import { t } from '../../ui/tokens.ts';
import { UsageTable } from '../shared/usage.tsx';
import { duration, EXEC_TONE, execKey, formatBytes, usageCost, usageTokens } from './run-model.ts';

const listCss = css({
  display: 'grid',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  overflow: 'hidden',
  '& > li': { borderTop: `1px solid ${t.border}`, minWidth: '0' },
  '& > li:first-child': { borderTop: '0' },
  '& > li[data-selected="true"]': { boxShadow: `inset 2px 0 0 ${t.accent}` },
});

const headCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: `4px ${t.s2}`,
  width: '100%',
  minHeight: '40px',
  padding: `6px ${t.s4}`,
  border: '0',
  borderRadius: '0',
  background: 'transparent',
  color: t.text,
  fontSize: t.textMd,
  textAlign: 'left',
  justifyContent: 'flex-start',
  '&:hover': { background: t.bgHover },
  '&:focus-visible': { boxShadow: `inset ${t.focus}` },
  '& .chev': { display: 'inline-flex', color: t.textFaint, transition: `transform ${t.fast} ${t.ease}` },
  '&[aria-expanded="true"] .chev': { transform: 'rotate(90deg)' },
  '& .step': { minWidth: '52px', color: t.textFaint, fontFamily: t.mono, fontSize: t.textSm },
  '& .sep': { color: t.textFaint },
  '& .who': { display: 'inline-flex', alignItems: 'center', gap: '6px', color: t.textMuted },
});

const detailCss = css({
  display: 'grid',
  gap: t.s4,
  padding: `${t.s3} ${t.s4} ${t.s4} 44px`,
  borderTop: `1px dashed ${t.border}`,
  background: t.bgApp,
  '& h4': { fontSize: t.textSm, fontWeight: '600', color: t.textMuted, textTransform: 'uppercase', letterSpacing: '0.04em', marginBottom: t.s2 },
  '& section > p': { margin: '0 0 4px' },
});

/** A row of actions; a PromptView's error and prompt text (when shown) take a full line under it. */
const actionsCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: t.s2,
  minWidth: '0',
  '& > pre, & > [role="alert"]': { order: '1', flexBasis: '100%', minWidth: '0' },
});
export const promptBox = actionsCss;

export const promptCss = css({
  margin: '0',
  maxHeight: '420px',
  overflow: 'auto',
  padding: t.s3,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  fontFamily: t.mono,
  fontSize: t.textSm,
  lineHeight: '1.55',
  whiteSpace: 'pre-wrap',
  overflowWrap: 'anywhere',
});

/** "Show the exact prompt" for one turn: reads it from the core and shows it as plain text (never as HTML). */
export function PromptView(handle: Handle<{ taskId: string; runId: string; exec: ExecView; testId?: string }>) {
  const s = services(handle);
  const act = action(handle);
  let prompt: string | undefined;
  return () => {
    const { taskId, runId, exec: e, testId = 'prompt-text' } = handle.props;
    return (
      <>
        <Button
          size="sm"
          icon={ScrollText}
          disabled={act.busy === 'prompt'}
          mix={[
            on('click', async () => {
              const p = await act.run('prompt', () => s.client.runPrompt(taskId, runId, e.nodeId, e.step, e.attempt));
              if (p !== undefined) {
                prompt = p;
                void handle.update();
              }
            }),
          ]}
        >
          Show the exact prompt
        </Button>
        <ErrorNote error={act.error} what="Could not read the prompt" />
        {prompt !== undefined ? (
          <pre mix={promptCss} data-testid={testId} tabIndex={0} aria-label="The exact prompt">
            {prompt}
          </pre>
        ) : null}
      </>
    );
  };
}

/** Who decided the model of a turn, and why (the routing decision recorded for it). */
export function DecisionSummary(handle: Handle<{ d: RoutingDecisionView }>) {
  return () => {
    const d = handle.props.d;
    return (
      <>
        <p>
          Decided by <strong>{d.decidedBy}</strong>
          {d.model ? (
            <>
              : <code>{d.model}</code>
              {d.effort ? ` (${d.effort})` : ''}
            </>
          ) : null}
          {d.escalatedFrom ? <> · escalated from {d.escalatedFrom}</> : null}
          {d.estCostUsd !== undefined && d.estCostUsd !== null ? <> · estimated {formatCost(d.estCostUsd)}</> : null}
        </p>
        {d.reason ? <p mix={muted}>{d.reason}</p> : null}
      </>
    );
  };
}

export function Turns(
  handle: Handle<{ x: RunExplanation; taskId: string; agents: readonly Agent[]; open?: string; highlight?: string; onToggle: (key: string | undefined) => void }>,
) {
  const s = services(handle);
  return () => {
    const { x, taskId, agents, open, highlight, onToggle } = handle.props;
    if (x.executions.length === 0) return <p mix={muted}>No node has run yet.</p>;
    return (
      <ol mix={listCss} aria-label="Turns">
        {x.executions.map((e) => {
          const key = execKey(e);
          const isOpen = open === key;
          const name = e.agentId ? agentName(e.agentId, agents) || e.agentId : '';
          return (
            <li key={key} data-testid="turn" data-node={e.nodeId} data-key={key} data-selected={String(highlight === e.nodeId)}>
              <button type="button" mix={[headCss, on('click', () => onToggle(isOpen ? undefined : key))]} aria-expanded={isOpen ? 'true' : 'false'}>
                <span class="chev" aria-hidden="true">
                  <Icon icon={ChevronRight} size={14} />
                </span>
                <span class="step">step {e.step}</span>
                <strong>{e.nodeId}</strong>
                {e.attempt > 1 ? <span mix={muted}>attempt {e.attempt}</span> : null}
                <Badge tone={EXEC_TONE[e.status] ?? 'muted'}>{e.status}</Badge>
                <span mix={muted}>{duration(e.started, e.finished)}</span>
                {name ? (
                  <span class="who">
                    <span class="sep">·</span>
                    <Avatar id={e.agentId!} name={name} size={16} />
                    {name}
                  </span>
                ) : null}
                {e.decision?.model ? (
                  <span mix={muted}>
                    <span class="sep">· </span>
                    {e.decision.model}
                  </span>
                ) : null}
                {e.usage && e.usage.length ? (
                  <span mix={muted}>
                    <span class="sep">· </span>
                    {usageTokens(e)} tokens · {formatCost(usageCost(e))}
                  </span>
                ) : null}
              </button>
              {isOpen ? <TurnDetail e={e} taskId={taskId} runId={x.run!.id} openLogs={() => s.ui.openDock('logs')} /> : null}
            </li>
          );
        })}
      </ol>
    );
  };
}

function TurnDetail(handle: Handle<{ e: ExecView; taskId: string; runId: string; openLogs: () => void }>) {
  return () => {
    const { e, taskId, runId, openLogs } = handle.props;
    return (
      <div mix={[detailCss, ref((n) => (n as HTMLElement).scrollIntoView?.({ block: 'nearest' }))]}>
        {e.error ? (
          <p>
            <Badge tone="err">error</Badge> {e.error}
          </p>
        ) : null}
        {e.decision ? (
          <section aria-label="Why this model">
            <h4>Why this model</h4>
            <DecisionSummary d={e.decision} />
          </section>
        ) : null}
        {e.manifest && e.manifest.length ? (
          <section aria-label="What it was given">
            <h4>What it was given ({formatBytes(e.includedBytes ?? 0)} included)</h4>
            <div mix={tableCard}>
              <table aria-label="Context manifest">
                <thead>
                  <tr>
                    <th scope="col">Source</th>
                    <th scope="col">Included</th>
                    <th scope="col">Size</th>
                    <th scope="col">Priority</th>
                    <th scope="col">Why</th>
                  </tr>
                </thead>
                <tbody>
                  {e.manifest.map((m, i) => (
                    <tr key={`${m.source}-${i}`} data-testid="manifest-item" data-included={String(m.included)}>
                      <td>
                        <code>{m.source}</code>
                      </td>
                      <td>{m.included ? <Badge tone="ok">yes</Badge> : <Badge>no</Badge>}</td>
                      <td mix={mono}>{m.bytes ? formatBytes(m.bytes) : ''}</td>
                      <td mix={mono}>{m.priority}</td>
                      <td>{m.reason}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        ) : null}
        {e.hasPrompt || e.logId || e.safePoint ? (
          <div mix={actionsCss}>
            {e.hasPrompt ? <PromptView taskId={taskId} runId={runId} exec={e} /> : null}
            {e.logId ? (
              <Button size="sm" variant="ghost" mix={[on('click', () => openLogs())]}>
                Open its stream in the Logs tab
              </Button>
            ) : null}
            {e.safePoint ? (
              <span mix={muted}>
                safe point <code>{e.safePoint.slice(0, 10)}</code>
              </span>
            ) : null}
          </div>
        ) : null}
        {e.usage && e.usage.length ? (
          <section aria-label="Usage">
            <h4>Usage</h4>
            <UsageTable rows={e.usage} />
          </section>
        ) : null}
      </div>
    );
  };
}
