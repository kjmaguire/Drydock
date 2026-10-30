// The task's acceptance criteria, each with its own status (decision P125): open, met, not met or waived, the line that says what
// showed it (a check, a review, a person), an agent's claim that nothing has confirmed, the check the criterion names to confirm
// it and the review findings about how it is written (P128, shown beside it since P129). The person gives their word on a
// criterion from its "..." menu: met, not met, waive (with a reason, asked for in place) or reopen. A claim never makes a
// criterion met; only a check or a person confirms it, and only a person waives it.

import { css, on, type Handle } from 'remix/component';
import { Ban, Check, Circle, X } from 'lucide';
import type { CriterionStatus } from '../../api/types.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { MoreMenu, type MenuItem } from '../../ui/menu.tsx';
import { t } from '../../ui/tokens.ts';
import { STATUS_WORDS, type CriterionRow, type CriterionStatusMark } from './task-page.ts';

const listCss = css({
  display: 'flex',
  flexDirection: 'column',
  gap: '8px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& > li': { display: 'grid', gridTemplateColumns: 'auto minmax(0, 1fr) auto', columnGap: '10px', rowGap: '2px', alignItems: 'start', lineHeight: '20px' },
  '& > li > svg': { marginTop: '2px' },
  '& > li > .text': { overflowWrap: 'anywhere' },
  '& > li > .why, & > li > .claim, & > li > .check, & > li > .finding, & > li > form': { gridColumn: '2 / 4', fontSize: t.textSm, color: t.textMuted, overflowWrap: 'anywhere' },
  '& > li > .claim, & > li > .finding[data-severity="medium"]': { color: t.warning },
  '& > li[data-state="met"] > svg': { color: t.success },
  '& > li[data-state="not_met"] > svg': { color: t.danger },
  '& > li[data-state="waived"] > svg': { color: t.textMuted },
  '& > li[data-state="open"] > svg': { color: t.textFaint },
  '& > li[data-state="open"] > .text, & > li[data-state="waived"] > .text': { color: t.textMuted },
  '& > li > form': { display: 'flex', flexWrap: 'wrap', alignItems: 'end', gap: t.s2, marginTop: '4px' },
  '& > li > form > *:first-child': { flex: '1 1 240px' },
});

const ICON: Record<CriterionStatusMark, typeof Check> = { met: Check, not_met: X, waived: Ban, open: Circle };

export function CriteriaList(handle: Handle<{ taskId: string; rows: CriterionRow[]; onChanged: (cs: CriterionStatus[]) => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  let waiving = -1; // the criterion whose reason is being asked for
  let reason = '';
  const set = (fn: () => void) => {
    fn();
    handle.update();
  };
  const mark = (index: number, m: 'met' | 'not_met' | 'waived' | 'open', note = '') =>
    void act.run(`${m}:${index}`, async () => {
      const cs = await s.client.taskCriterionMark(handle.props.taskId, index, m, note);
      waiving = -1;
      reason = '';
      handle.props.onChanged(cs);
    });

  return () => {
    const { rows } = handle.props;
    const ro = s.workspace.license;
    const busy = act.busy !== '';
    const items = (c: CriterionRow): MenuItem[] => [
      { name: 'met', label: 'Mark as met', icon: Check, disabled: busy || ro.readOnly || (c.personal && c.status === 'met') },
      { name: 'not_met', label: 'Mark as not met', icon: X, disabled: busy || (c.personal && c.status === 'not_met') },
      { name: 'waive', label: 'Waive...', icon: Ban, disabled: busy || ro.readOnly || c.status === 'waived' },
      { name: 'open', label: 'Reopen', icon: Circle, disabled: busy || !c.personal, separatorBefore: true },
    ];
    return (
      <div>
        <ErrorNote error={act.error} what="Not saved" />
        <ul mix={listCss} aria-label="Acceptance criteria" data-testid="task-criteria">
          {rows.map((c) => {
            const name = c.id || `criterion ${c.index + 1}`;
            return (
              <li key={c.index} data-state={c.status} title={`${c.id ? `${c.id}: ` : ''}${STATUS_WORDS[c.status]}`}>
                <Icon icon={ICON[c.status]} size={15} strokeWidth={2.2} label={STATUS_WORDS[c.status]} />
                <span class="text">{c.text}</span>
                <MoreMenu
                  label={`Mark ${name}`}
                  testId={`criterion-${c.index}`}
                  items={items(c)}
                  onSelect={(n) => (n === 'waive' ? set(() => ((waiving = c.index), (reason = ''))) : mark(c.index, n as 'met' | 'not_met' | 'open'))}
                />
                {c.why ? (
                  <span class="why" data-testid="criterion-why">
                    {c.why}
                  </span>
                ) : null}
                {c.claim ? (
                  <span class="claim" data-testid="criterion-claim">
                    {c.claim}
                  </span>
                ) : null}
                {c.check ? (
                  <span class="check" data-testid="criterion-check" title={`A passing ${c.check} check confirms it, a failing one shows it not met`}>
                    Confirmed by the {c.check} check
                  </span>
                ) : null}
                {c.findings.map((f) => (
                  <span key={f.text} class="finding" data-testid="criterion-finding" data-severity={f.severity}>
                    Review finding ({f.severity}): {f.text}
                  </span>
                ))}
                {waiving === c.index ? (
                  <form
                    aria-label={`Waive ${name}`}
                    mix={on('submit', (e) => {
                      e.preventDefault();
                      if (reason.trim()) mark(c.index, 'waived', reason.trim());
                    })}
                  >
                    <Field label="Why is it waived?">
                      {(id) => <input id={id} type="text" value={reason} maxLength={600} autofocus mix={on('input', (e) => set(() => (reason = e.currentTarget.value)))} />}
                    </Field>
                    <Button type="submit" variant="primary" size="sm" disabled={busy || !reason.trim()}>
                      Waive
                    </Button>
                    <Button size="sm" variant="ghost" mix={on('click', () => set(() => (waiving = -1)))}>
                      Cancel
                    </Button>
                  </form>
                ) : null}
              </li>
            );
          })}
        </ul>
      </div>
    );
  };
}
