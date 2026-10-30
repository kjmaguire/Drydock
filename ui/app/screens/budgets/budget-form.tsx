// The add/edit form for a budget (shown in a dialog). A limit field holds a plain number or nothing: text that is not a number
// is an error under its field and nothing is sent (M25), so a mistyped limit is never saved as "no limit". The core validates the
// rest: a budget without any limit is refused and its words are shown verbatim. A workflow budget (P118) names one of the
// workflows the core runs, chosen from its list, and counts that workflow's runs in this project.

import { css, on, type Handle } from 'remix/component';
import type { Budget } from '../../api/extTypes.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { errorAttrs, Field, formActions, formGrid } from '../../ui/form.tsx';
import { Select, selectValues } from '../../ui/select.tsx';
import { BUDGET_SCOPES, formValues, parseBudgetForm, scopeIdAfter, type BudgetFormValues } from './budgets.ts';

const wide = css({ gridColumn: '1 / -1' });

export function BudgetForm(handle: Handle<{ projectId: string; initial?: Budget; onDone: () => void; onCancel?: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  const workflows = query(handle, { load: () => s.client.budgetWorkflows() });
  const v: BudgetFormValues = formValues(handle.props.projectId, handle.props.initial);
  // the text of a limit that is not a number, by field; an error goes when its field is edited
  const errors: Partial<Record<keyof BudgetFormValues, string>> = {};

  const submit = async (form: HTMLFormElement) => {
    const { projectId, initial, onDone } = handle.props;
    const parsed = parseBudgetForm(v, projectId, initial);
    for (const k of Object.keys(errors) as (keyof typeof errors)[]) delete errors[k];
    if (!parsed.ok) {
      Object.assign(errors, parsed.errors);
      act.clear();
      // the person is told under the field, and the keyboard goes to the first one that needs fixing
      handle.queueTask(() => form.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
      return;
    }
    const r = await act.run('save', () => s.client.budgetSet(parsed.request));
    if (r) onDone();
  };

  const text = (label: string, key: keyof BudgetFormValues, opts: { mode?: 'decimal' | 'numeric'; disabled?: boolean; required?: boolean; span?: boolean; placeholder?: string } = {}) => (
    <div mix={opts.span ? wide : undefined}>
      <Field label={label} error={errors[key]}>
        {(id) => (
          <input
            id={id}
            inputMode={opts.mode}
            value={v[key]}
            disabled={opts.disabled}
            required={opts.required}
            placeholder={opts.placeholder}
            spellcheck={false}
            {...errorAttrs(id, errors[key])}
            mix={on('input', (e) => {
              v[key] = e.currentTarget.value;
              delete errors[key];
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
    // the workflow scope was chosen before the list arrived: it starts on the first workflow, as choosing it now would
    if (v.scope === 'workflow' && !v.scopeId && workflows.data?.length) v.scopeId = workflows.data[0]!;
    return (
      <form
        mix={[
          formGrid,
          on('submit', (e) => {
            e.preventDefault();
            void submit(e.currentTarget);
          }),
        ]}
        aria-label={editing ? 'Edit budget' : 'New budget'}
      >
        <div mix={wide}>
          <ErrorNote error={act.error} what="Budget not saved" />
        </div>
        <Field label="Scope">
          {(id) => (
            <Select
              id={id}
              value={v.scope}
              disabled={editing}
              options={selectValues(BUDGET_SCOPES)}
              onChange={(sc) => {
                v.scopeId = scopeIdAfter(v.scope, sc, v.scopeId, handle.props.projectId, workflows.data ?? []);
                v.scope = sc;
                delete errors.scopeId;
                void handle.update();
              }}
            />
          )}
        </Field>
        {v.scope === 'workflow' && (workflows.data?.length ?? 0) > 0 ? (
          <Field label="Workflow (its runs in this project)">
            {(id) => (
              <Select
                id={id}
                value={v.scopeId}
                disabled={editing}
                options={selectValues(workflows.data!.includes(v.scopeId) || !v.scopeId ? workflows.data! : [v.scopeId, ...workflows.data!])}
                onChange={(wf) => {
                  v.scopeId = wf;
                  void handle.update();
                }}
              />
            )}
          </Field>
        ) : (
          text(v.scope === 'workflow' ? 'Workflow (its runs in this project)' : 'Scope id (an id, or * for every task, run or agent)', 'scopeId', { disabled: editing, required: true })
        )}
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
