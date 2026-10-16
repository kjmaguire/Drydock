// Budgets for the selected project: each with its limits, the state in words (ok, warning, paused at 100%), a progress bar
// that repeats the number, and the spend with its provenance. Unknown-cost calls are counted, never priced at zero. Only
// the human changes a budget.

import { css, on, type Handle } from 'remix/component';
import { CircleCheck, CirclePause, FolderOpen, Infinity as InfinityIcon, PiggyBank, Plus, TriangleAlert } from 'lucide';
import type { BudgetRow, BudgetStatus } from '../../api/extTypes.ts';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Badge, muted } from '../../ui/data.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { Empty, ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { card, Page, spacer } from '../../ui/layout.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { pctText } from '../shared/budget-pause.tsx';
import { BudgetForm } from './budget-form.tsx';
import { limitsText, plural, stateTone } from './budgets.ts';
import { Meter } from './meter.tsx';

/** State with text and an icon, never color alone: ok, warning at the warn percentage, paused at 100%. */
export function StateChip(handle: Handle<{ s: BudgetStatus }>) {
  return () => {
    const s = handle.props.s;
    if (s.state === 'exceeded')
      return (
        <Badge tone="err" title="The next agent turn does not start until a human resumes it">
          <Icon icon={CirclePause} size={12} />
          paused at 100%
        </Badge>
      );
    if (s.state === 'warning')
      return (
        <Badge tone="warn">
          <Icon icon={TriangleAlert} size={12} />
          warning at {pctText(s.budget.warnPct)}
        </Badge>
      );
    return (
      <Badge tone="ok">
        <Icon icon={CircleCheck} size={12} />
        ok
      </Badge>
    );
  };
}

const spendCss = css({
  display: 'grid',
  gap: '4px',
  fontSize: t.textSm,
  '& strong': { fontVariantNumeric: 'tabular-nums' },
});
const unknownCss = css({ padding: '6px 10px', borderRadius: t.rMd, background: t.warningSoft, color: t.text });

/** Spend with its provenance. Unknown-cost calls are counted, never priced at zero. */
export function SpendLine(handle: Handle<{ s: BudgetStatus }>) {
  return () => {
    const s = handle.props.s;
    const sp = s.spend;
    return (
      <div mix={spendCss} data-testid="spend">
        <div>
          Known cost: <strong>${(sp.reportedUsd + sp.estimatedUsd).toFixed(4)}</strong> ({s.costLabel}
          {sp.reportedUsd > 0 ? `; reported $${sp.reportedUsd.toFixed(4)}` : ''}
          {sp.estimatedUsd > 0 ? `; estimated $${sp.estimatedUsd.toFixed(4)}` : ''})
        </div>
        {sp.unknownCostRows > 0 ? (
          <div mix={unknownCss} data-testid="unknown-cost">
            {sp.unknownCostRows} call{sp.unknownCostRows === 1 ? '' : 's'} with unknown cost are counted but not priced at zero
            {s.budget.limitUsd != null ? `, so the dollar percentage (${pctText(s.pctUsd)}) is a lower bound` : ''}.
          </div>
        ) : null}
        <div mix={muted}>
          {plural(sp.turns, 'turn')}, {(sp.inputTokens + sp.outputTokens).toLocaleString()} tokens ({sp.cachedTokens.toLocaleString()} cached)
        </div>
      </div>
    );
  };
}

const gridCss = css({ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))', gap: t.s3, alignItems: 'start' });
const cardCss = css({ display: 'grid', gap: t.s3, alignContent: 'start' });
const headCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  flexWrap: 'wrap',
  '& h3': { display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textMd, fontWeight: '600', minWidth: '0' },
  '& h3 code': { fontWeight: '400', overflowWrap: 'anywhere' },
});
const scopeIcon = css({ display: 'grid', placeItems: 'center', width: '28px', height: '28px', flex: 'none', borderRadius: t.rMd });
const usedCss = css({ display: 'flex', alignItems: 'baseline', gap: t.s2, flexWrap: 'wrap', fontSize: t.textSm, color: t.textMuted, '& strong': { fontSize: t.textLg, color: t.text, fontVariantNumeric: 'tabular-nums' } });
const footCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', paddingTop: t.s3, borderTop: `1px solid ${t.border}` });

function BudgetCard(handle: Handle<{ row: BudgetRow; onChanged: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  let editing = false;
  let confirm = false;

  return () => {
    const { row: r, onChanged } = handle.props;
    const b = r.budget;
    const st = r.status;
    const tn = st ? stateTone(st.state) : 'muted';
    return (
      <article mix={[card, cardCss]} data-testid="budget" data-scope={b.scope} data-state={st?.state ?? 'template'}>
        <header mix={headCss}>
          <span mix={scopeIcon} aria-hidden="true" style={{ background: tone[tn].bg, color: tone[tn].fg }}>
            <Icon icon={st ? PiggyBank : InfinityIcon} size={15} />
          </span>
          <h3>
            {b.scope} <code>{b.scopeId}</code>
          </h3>
          <span mix={spacer} />
          <Badge tone={b.enabled ? 'ok' : 'muted'}>{b.enabled ? 'enabled' : 'disabled'}</Badge>
          {st ? (
            <StateChip s={st} />
          ) : (
            <Badge title="A ceiling for every instance; each task, run or agent is judged on its own">ceiling for every {b.scope}</Badge>
          )}
        </header>
        <p>
          Limit: <strong>{limitsText(b)}</strong>, warns at {pctText(b.warnPct)}, pauses at 100%.
        </p>
        {b.note ? <p mix={muted}>{b.note}</p> : null}
        {st ? (
          <>
            <div mix={usedCss}>
              <strong>{pctText(st.pct)}</strong>
              <span>
                Used {pctText(st.pct)} of the {st.binding ?? ''} limit.{' '}
                {st.pctUsd != null ? `USD ${pctText(st.pctUsd)}${st.costComplete ? '' : ' (lower bound)'}` : ''}
                {st.pctTokens != null ? ` tokens ${pctText(st.pctTokens)}` : ''}
                {st.pctTurns != null ? ` turns ${pctText(st.pctTurns)}` : ''}
              </span>
            </div>
            <Meter value={st.pct} tone={tn} mark={b.warnPct} label={`${pctText(st.pct)} of the limit used`} />
            <SpendLine s={st} />
          </>
        ) : null}
        <ErrorNote error={act.error} />
        <div mix={footCss}>
          <Button
            size="sm"
            mix={on('click', () => {
              editing = true;
              void handle.update();
            })}
          >
            Edit limits
          </Button>
          <Button
            size="sm"
            disabled={act.busy !== ''}
            mix={on('click', async () => {
              await act.run('toggle', () => s.client.budgetSet({ ...b, enabled: !b.enabled }));
              onChanged();
            })}
          >
            {b.enabled ? 'Disable' : 'Enable'}
          </Button>
          <span mix={spacer} />
          {confirm ? (
            <>
              <Button
                size="sm"
                variant="danger"
                disabled={act.busy !== ''}
                mix={on('click', async () => {
                  await act.run('delete', () => s.client.budgetDelete(b.id));
                  onChanged();
                })}
              >
                Yes, delete
              </Button>
              <Button
                size="sm"
                mix={on('click', () => {
                  confirm = false;
                  void handle.update();
                })}
              >
                Keep
              </Button>
            </>
          ) : (
            <Button
              size="sm"
              variant="ghost"
              mix={on('click', () => {
                confirm = true;
                void handle.update();
              })}
            >
              Delete
            </Button>
          )}
        </div>
        <Dialog
          open={editing}
          title={`Edit budget: ${b.scope} ${b.scopeId}`}
          onClose={() => {
            editing = false;
            void handle.update();
          }}
        >
          <BudgetForm
            projectId={b.projectId ?? ''}
            initial={b}
            onDone={() => {
              editing = false;
              void handle.update();
              onChanged();
            }}
            onCancel={() => {
              editing = false;
              void handle.update();
            }}
          />
        </Dialog>
      </article>
    );
  };
}

const introCss = css({ color: t.textMuted, fontSize: t.textMd, maxWidth: '820px' });

export function BudgetsScreen(handle: Handle) {
  const s = services(handle);
  watch(handle, s.workspace);
  const q = query(handle, {
    load: () => s.client.budgetList(s.workspace.projectId!),
    when: (e) => e.type.startsWith('budget.') || e.type.startsWith('task.') || e.type === 'room.message_posted',
    key: () => s.workspace.projectId,
    enabled: () => !!s.workspace.projectId,
  });
  let adding = false;
  const close = () => {
    adding = false;
    void handle.update();
  };

  return () => {
    const projectId = s.workspace.projectId;
    const project = s.workspace.project;
    const rows = q.data ?? [];
    return (
      <Page
        title="Budgets"
        icon={PiggyBank}
        subtitle={project ? project.name : undefined}
        actions={
          projectId ? (
            <Button
              variant="primary"
              icon={Plus}
              mix={on('click', () => {
                adding = true;
                void handle.update();
              })}
            >
              Add budget
            </Button>
          ) : undefined
        }
      >
        <p mix={introCss}>
          A budget warns at its warning percentage (80% by default). At 100% no new agent turn starts: a task's run waits for you in Needs you, where you can raise the
          limit or approve one more turn, and agents stop answering in the room and in direct messages until the limit is raised. Your own chats are not counted.
          Only you can change a budget. Costs are reported, estimated or unknown and are never merged: calls with an unknown
          cost are counted, not priced at zero.
        </p>
        {!projectId ? (
          s.workspace.projects.data === undefined ? (
            <Loading />
          ) : (
            <Empty title="No project selected" icon={FolderOpen}>
              <p>Budgets belong to a project. Choose or create one first.</p>
            </Empty>
          )
        ) : (
          <>
            <ErrorNote error={q.error} what="Could not list budgets" />
            {q.loading && !q.data ? <Loading /> : null}
            {q.data && rows.length === 0 && !adding ? (
              <Empty title="No budgets" icon={PiggyBank}>
                <p>Usage is shown, but nothing limits it.</p>
              </Empty>
            ) : null}
            {rows.length > 0 ? (
              <div mix={gridCss}>
                {rows.map((r) => (
                  <BudgetCard key={r.budget.id} row={r} onChanged={() => q.reload()} />
                ))}
              </div>
            ) : null}
            <Dialog open={adding} title="New budget" onClose={close}>
              <BudgetForm
                projectId={projectId}
                onDone={() => {
                  close();
                  q.reload();
                }}
                onCancel={close}
              />
            </Dialog>
          </>
        )}
      </Page>
    );
  };
}
