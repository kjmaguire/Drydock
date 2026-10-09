// The add/edit form for a budget (shown in a dialog). The core validates: a budget without any limit is refused and its
// words are shown verbatim.

import { css, on, type Handle } from 'remix/component';
import type { Budget } from '../../api/extTypes.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { budgetRequest, formValues, type BudgetFormValues } from './budgets.ts';

const SCOPES = ['project', 'task', 'run', 'agent'];
const wide = css({ gridColumn: '1 / -1' });

export function BudgetForm(handle: Handle<{ projectId: string; initial?: Budget; onDone: () => void; onCancel?: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  const v: BudgetFormValues = formValues(handle.props.projectId, handle.props.initial);

  const submit = async () => {
    const { projectId, initial, onDone } = handle.props;
    const r = await act.run('save', () => s.client.budgetSet(budgetRequest(v, projectId, initial)));
    if (r) onDone();
  };

  const text = (label: string, key: keyof BudgetFormValues, opts: { mode?: 'decimal' | 'numeric'; disabled?: boolean; required?: boolean; span?: boolean; placeholder?: string } = {}) => (
    <div mix={opts.span ? wide : undefined}>
      <Field label={label}>
        {(id) => (
          <input
            id={id}
            inputMode={opts.mode}
            value={v[key]}
            disabled={opts.disabled}
            required={opts.required}
            placeholder={opts.placeholder}
            spellcheck={false}
            mix={on('input', (e) => {
              v[key] = e.currentTarget.value;
              void handle.update();
            })}
          />
        )}
      </Field>
    </div>
  );

  return () => {
    const { initial, onCancel } = handle.props;
    const editing = !!initial;
    return (
      <form
        mix={[
          formGrid,
          on('submit', (e) => {
            e.preventDefault();
            void submit();
          }),
        ]}
        aria-label={editing ? 'Edit budget' : 'New budget'}
      >
        <div mix={wide}>
          <ErrorNote error={act.error} what="Budget not saved" />
        </div>
        <Field label="Scope">
          {(id) => (
            <select
              id={id}
              value={v.scope}
              disabled={editing}
              mix={on('change', (e) => {
                v.scope = e.currentTarget.value;
                void handle.update();
              })}
            >
              {SCOPES.map((sc) => (
                <option key={sc} value={sc} selected={sc === v.scope}>
                  {sc}
                </option>
              ))}
            </select>
          )}
        </Field>
        {text('Scope id (an id, or * for every task, run or agent)', 'scopeId', { disabled: editing, required: true })}
        {text('Limit in USD', 'usd', { mode: 'decimal', placeholder: 'no limit' })}
        {text('Limit in tokens', 'tokens', { mode: 'numeric', placeholder: 'no limit' })}
        {text('Limit in turns', 'turns', { mode: 'numeric', placeholder: 'no limit' })}
        {text('Warn at (percent)', 'warn', { mode: 'numeric' })}
        {text('Note', 'note', { span: true })}
        <div mix={formActions}>
          <Button type="submit" variant="primary" disabled={act.busy !== ''}>
            {editing ? 'Save changes' : 'Add budget'}
          </Button>
          {onCancel ? <Button mix={on('click', () => onCancel())}>Cancel</Button> : null}
        </div>
      </form>
    );
  };
}
