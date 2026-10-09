// The team page's forms: add an agent (from a saved template or from scratch), edit one in place, remove one.

import { css, on, type Handle } from 'remix/component';
import { BookmarkPlus, LayoutTemplate, UserPlus } from 'lucide';
import type { AdapterInfo, Agent, AgentSpec, AgentUpdate, RouteModel } from '../../api/types.ts';
import { routes as appRoutes } from '../../routes.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { ClaudePracticesPanel, ModelEffortFields, RouteModelsField, claudePractices } from './fields.tsx';
import { PURPOSE_HELP, ROLE_SUGGESTIONS, adapterOption, appliedNote, routesForUpdate } from './team.ts';

const full = css({ gridColumn: '1 / -1' });

function RoleSuggestions(handle: Handle<{ id: string }>) {
  return () => (
    <datalist id={handle.props.id}>
      {ROLE_SUGGESTIONS.map((r) => (
        <option key={r} value={r} />
      ))}
    </datalist>
  );
}

/** Create an agent from scratch. */
export function AgentForm(handle: Handle<{ projectId: string; adapters: AdapterInfo[]; onCreated: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const roleList = `roles-${handle.id}`;
  let name = '';
  let role = '';
  let purpose = '';
  let adapter = '';
  let model = '';
  let effort = '';
  let practices = true;
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const chosen = () => handle.props.adapters.find((a) => a.id === (adapter || handle.props.adapters[0]?.id));
  const pr = claudePractices(handle, {
    adapter: () => chosen()?.id ?? '',
    role: () => role,
    fields: { model: () => model, effort: () => effort, set: (m, e) => set(() => ((model = m), (effort = e))) },
  });

  const submit = async () => {
    const a = chosen();
    const adapterId = a?.id ?? '';
    const spec: AgentSpec = {
      projectId: handle.props.projectId,
      name: name.trim(),
      role: role.trim(),
      purpose: purpose.trim(),
      adapter: adapterId,
      model: a?.capabilities?.modelSelection === true ? model.trim() : '',
      effort: a?.capabilities?.effortControl === true ? effort.trim() : '',
    };
    if (adapterId === 'claude') spec.practices = practices;
    const ok = await act.run('create', async () => {
      await s.client.agentCreate(spec);
      return true;
    });
    if (!ok) return;
    name = role = purpose = model = effort = '';
    void handle.update();
    handle.props.onCreated();
  };

  return () => {
    const { adapters } = handle.props;
    const ro = s.workspace.license;
    const a = chosen();
    const adapterId = a?.id ?? '';
    const info = pr.info;
    return (
      <form
        mix={[formGrid, on('submit', (e) => (e.preventDefault(), void submit()))]}
        aria-label="New agent"
        data-testid="agent-form"
      >
        <div mix={full}>
          <ErrorNote error={act.error} what="Agent not created" />
          <ReadOnlyNote />
        </div>
        <Field label="Agent name">
          {(id) => <input id={id} data-autofocus value={name} required autocomplete="off" mix={on('input', (e) => set(() => (name = e.currentTarget.value)))} />}
        </Field>
        <Field label="Role">
          {(id) => (
            <>
              <input id={id} value={role} list={roleList} required autocomplete="off" mix={on('input', (e) => set(() => (role = e.currentTarget.value)))} />
              <RoleSuggestions id={roleList} />
            </>
          )}
        </Field>
        <Field label="Adapter">
          {(id) => (
            <select
              id={id}
              aria-label="Adapter"
              value={adapterId}
              required
              mix={on('change', (e) => set(() => ((adapter = e.currentTarget.value), (model = ''), (effort = ''))))}
            >
              {adapters.map((x) => (
                <option key={x.id} value={x.id} selected={x.id === adapterId}>
                  {adapterOption(x)}
                </option>
              ))}
            </select>
          )}
        </Field>
        <ModelEffortFields
          caps={a?.capabilities}
          model={model}
          effort={effort}
          onModel={(v) => set(() => (model = v))}
          onEffort={(v) => set(() => (effort = v))}
        />
        <div mix={full}>
          <Field label="Purpose (optional)">
            {(id) => (
              <textarea id={id} value={purpose} rows={3} maxLength={4000} placeholder={PURPOSE_HELP} mix={on('input', (e) => set(() => (purpose = e.currentTarget.value)))} />
            )}
          </Field>
        </div>
        <ClaudePracticesPanel info={info} on={practices} onChange={(v) => set(() => (practices = v))} />
        <div mix={formActions}>
          <Button
            type="submit"
            variant="primary"
            icon={UserPlus}
            disabled={!!act.busy || ro.readOnly || !name.trim() || !role.trim() || !adapterId}
            title={ro.readOnly ? ro.reason : undefined}
          >
            Create agent
          </Button>
        </div>
      </form>
    );
  };
}

const templateRow = css({
  display: 'grid',
  gridTemplateColumns: 'minmax(200px, 2fr) minmax(160px, 1fr) auto',
  gap: t.s3,
  alignItems: 'end',
  '@media (max-width: 720px)': { gridTemplateColumns: '1fr' },
});

const hintCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, margin: '0', color: t.textMuted, fontSize: t.textSm });

/** Drop a saved team member into this project: an ordinary agent is made from the template, with the MCP servers it lists. */
export function FromTemplate(handle: Handle<{ projectId: string; onCreated: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const q = query(handle, { load: () => s.client.agentTemplateList(), when: (e) => e.type.startsWith('agenttemplate.') });
  const act = action(handle);
  let pick = '';
  let name = '';
  let note = '';

  const apply = async (templateId: string) => {
    note = '';
    const r = await act.run('apply', () => s.client.agentTemplateApply(templateId, handle.props.projectId, name.trim() || undefined));
    if (!r) return;
    note = appliedNote(r);
    name = '';
    void handle.update();
    handle.props.onCreated();
  };

  return () => {
    const templates = q.data ?? [];
    if (templates.length === 0) {
      return (
        <p mix={hintCss}>
          <Icon icon={LayoutTemplate} size={14} />
          <span>
            Team members you save as templates in the <a href={appRoutes.library.href()}>Library</a> can be added here with one click.
          </span>
        </p>
      );
    }
    const ro = s.workspace.license;
    const chosen = templates.some((x) => x.id === pick) ? pick : templates[0]!.id;
    return (
      <section aria-label="Add an agent from a template" style={{ display: 'grid', gap: '12px' }}>
        <ErrorNote error={act.error} what="Agent not created" />
        <div mix={templateRow}>
          <Field label="From a template">
            {(id) => (
              <select id={id} aria-label="Template" value={chosen} mix={on('change', (e) => ((pick = e.currentTarget.value), void handle.update()))}>
                {templates.map((x) => (
                  <option key={x.id} value={x.id} selected={x.id === chosen}>
                    {x.name} ({x.role}, {x.adapter})
                  </option>
                ))}
              </select>
            )}
          </Field>
          <Field label="Name (optional)">
            {(id) => (
              <input id={id} value={name} placeholder="the template's name" autocomplete="off" mix={on('input', (e) => ((name = e.currentTarget.value), void handle.update()))} />
            )}
          </Field>
          <Button variant="secondary" icon={LayoutTemplate} disabled={!!act.busy || ro.readOnly} title={ro.readOnly ? ro.reason : undefined} mix={on('click', () => void apply(chosen))}>
            Add to this team
          </Button>
        </div>
        {note ? (
          <Notice tone="ok" role="status">
            {note}
          </Notice>
        ) : null}
      </section>
    );
  };
}

const removeBtn = css({ marginLeft: 'auto', color: t.danger, '&:hover': { color: t.danger, background: t.dangerSoft } });
const removeRow = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: t.s2, gridColumn: '1 / -1', fontSize: t.textMd });

/** Edit an agent in place. The adapter is fixed; a changed model, effort or purpose applies to the agent's next session. */
export function AgentEditor(handle: Handle<{ agent: Agent; adapter: AdapterInfo | undefined; onDone: () => void; onCancel: () => void }>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const roleList = `roles-${handle.id}`;
  const a0 = handle.props.agent;
  let name = a0.name;
  let role = a0.role;
  let purpose = a0.purpose ?? '';
  let model = a0.model;
  let effort = a0.effort;
  let practices = a0.practices !== false;
  let routes: RouteModel[] = a0.routeModels ?? [];
  let confirmRemove = false;
  let savedAs = '';
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  // an existing agent keeps its model and effort: the recommendation is shown, not applied over them
  const pr = claudePractices(handle, { adapter: () => handle.props.agent.adapter, role: () => role });

  const finish = async (label: string, fn: () => Promise<unknown>) => {
    const ok = await act.run(label, async () => {
      await fn();
      return true;
    });
    if (ok) handle.props.onDone();
  };

  const save = () => {
    const agent = handle.props.agent;
    const u: AgentUpdate = { id: agent.id, name: name.trim(), role: role.trim(), purpose: purpose.trim(), model: model.trim(), effort: effort.trim() };
    if (agent.adapter === 'claude') u.practices = practices;
    const rm = routesForUpdate(routes, agent.routeModels);
    if (rm) u.routeModels = rm;
    return finish('save', () => s.client.agentUpdate(u));
  };

  const saveTemplate = async () => {
    const tpl = await act.run('template', () => s.client.agentTemplateFromAgent(handle.props.agent.id));
    if (tpl) set(() => (savedAs = tpl.name));
  };

  return () => {
    const { agent, adapter, onCancel } = handle.props;
    const ro = s.workspace.license;
    const busy = !!act.busy;
    const info = pr.info;
    return (
      <form mix={[formGrid, on('submit', (e) => (e.preventDefault(), void save()))]} aria-label={`Edit ${agent.name}`} data-testid="agent-editor">
        <div mix={full} style={{ display: 'grid', gap: '8px' }}>
          <ErrorNote error={act.error} what="Agent not changed" />
          {savedAs ? (
            <Notice tone="ok" role="status" testId="saved-as-template">
              Saved as the template {savedAs}. Find it in the <a href={appRoutes.library.href()}>Library</a>.
            </Notice>
          ) : null}
          <ReadOnlyNote />
          <p mix={muted} style={{ margin: '0', fontSize: 'var(--text-sm)' }}>
            Adapter <code>{agent.adapter}</code> cannot change: add a new agent to use a different CLI. Changes apply from the agent's next session.
          </p>
        </div>
        <Field label="Agent name">
          {(id) => <input id={id} value={name} required autocomplete="off" mix={on('input', (e) => set(() => (name = e.currentTarget.value)))} />}
        </Field>
        <Field label="Role">
          {(id) => (
            <>
              <input id={id} value={role} list={roleList} required autocomplete="off" mix={on('input', (e) => set(() => (role = e.currentTarget.value)))} />
              <RoleSuggestions id={roleList} />
            </>
          )}
        </Field>
        <ModelEffortFields
          caps={adapter?.capabilities}
          model={model}
          effort={effort}
          onModel={(v) => set(() => (model = v))}
          onEffort={(v) => set(() => (effort = v))}
        />
        <div mix={full}>
          <Field label="Purpose">
            {(id) => (
              <textarea id={id} value={purpose} rows={4} maxLength={4000} placeholder={PURPOSE_HELP} mix={on('input', (e) => set(() => (purpose = e.currentTarget.value)))} />
            )}
          </Field>
        </div>
        <ClaudePracticesPanel info={info} on={practices} onChange={(v) => set(() => (practices = v))} />
        <RouteModelsField routes={routes} onRoutes={(r) => set(() => (routes = r))} suggestions={adapter?.capabilities?.models ?? []} />
        <div mix={formActions}>
          <Button type="submit" variant="primary" disabled={busy || ro.readOnly || !name.trim() || !role.trim()} title={ro.readOnly ? ro.reason : undefined}>
            {act.busy === 'save' ? 'Saving...' : 'Save changes'}
          </Button>
          <Button disabled={busy} mix={on('click', () => onCancel())}>
            Cancel
          </Button>
          <Button variant="ghost" icon={BookmarkPlus} disabled={busy} mix={on('click', () => void saveTemplate())}>
            Save as a template
          </Button>
          {confirmRemove ? null : (
            <Button variant="ghost" disabled={busy} mix={[on('click', () => set(() => (confirmRemove = true))), removeBtn]}>
              Remove from team
            </Button>
          )}
        </div>
        {confirmRemove ? (
          <div mix={removeRow} role="group" aria-label="Confirm removal">
            <span>Remove {agent.name} from the team? Its history stays.</span>
            <Button variant="danger" disabled={busy} mix={on('click', () => void finish('remove', () => s.client.agentArchive(agent.id)))}>
              {act.busy === 'remove' ? 'Removing...' : 'Yes, remove'}
            </Button>
            <Button disabled={busy} mix={on('click', () => set(() => (confirmRemove = false)))}>
              Keep
            </Button>
          </div>
        ) : null}
      </form>
    );
  };
}

/** The confirmation behind a card's Remove button. */
export function RemoveAgent(handle: Handle<{ agent: Agent; onDone: () => void; onCancel: () => void }>) {
  const s = services(handle);
  const act = action(handle);
  const remove = async () => {
    const ok = await act.run('remove', async () => {
      await s.client.agentArchive(handle.props.agent.id);
      return true;
    });
    if (ok) handle.props.onDone();
  };
  return () => {
    const { agent, onCancel } = handle.props;
    return (
      <div style={{ display: 'grid', gap: '16px' }} data-testid="agent-remove">
        <ErrorNote error={act.error} what="Agent not removed" />
        <p>Remove {agent.name} from the team? Its history stays.</p>
        <div mix={formActions}>
          <Button variant="danger" disabled={!!act.busy} mix={on('click', () => void remove())}>
            {act.busy ? 'Removing...' : 'Yes, remove'}
          </Button>
          <Button disabled={!!act.busy} mix={on('click', () => onCancel())}>
            Keep
          </Button>
        </div>
      </div>
    );
  };
}
