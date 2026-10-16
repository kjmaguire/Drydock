// The team page's forms: add an agent (from a saved template or from scratch), edit one in place, remove one.

import { css, on, type Handle } from 'remix/component';
import { BookmarkPlus, LayoutTemplate, UserPlus } from 'lucide';
import type { AdapterInfo, Agent, AgentSpec, AgentUpdate, RouteModel } from '../../api/types.ts';
import { confirmationFor, isNeedsConfirmation, needsConfirmation, splitConfirmation } from '../../lib/permission.ts';
import { routes as appRoutes } from '../../routes.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Select } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { SkipApprovals } from '../shared/skip-approvals.tsx';
import { noSkillChanges, type SkillChanges } from '../skills/skills.ts';
import { ClaudePracticesPanel, ModelEffortFields, RouteModelsField, claudePractices } from './fields.tsx';
import { RunModeField } from './run-mode-field.tsx';
import { supportsWSL } from './run-mode.ts';
import { SkillsField } from './skills-field.tsx';
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
  let runMode = '';
  let wslDistro = '';
  let skills: SkillChanges = noSkillChanges();
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
    if (skills.add.length > 0) spec.skills = skills.add;
    if (supportsWSL(a?.capabilities) && runMode) {
      spec.runMode = runMode;
      if (runMode === 'wsl') spec.wslDistro = wslDistro;
    }
    const ok = await act.run('create', async () => {
      await s.client.agentCreate(spec);
      return true;
    });
    if (!ok) return;
    name = role = purpose = model = effort = runMode = wslDistro = '';
    skills = noSkillChanges();
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
            <Select
              id={id}
              label="Adapter"
              value={adapterId}
              options={adapters.map((x) => ({ value: x.id, label: adapterOption(x) }))}
              placeholder="Choose an agent CLI"
              // another adapter starts the model and effort over (the Select is quiet when the same one is chosen again)
              onChange={(v) => set(() => ((adapter = v), (model = ''), (effort = '')))}
            />
          )}
        </Field>
        <ModelEffortFields
          caps={a?.capabilities}
          model={model}
          effort={effort}
          onModel={(v) => set(() => (model = v))}
          onEffort={(v) => set(() => (effort = v))}
        />
        <RunModeField caps={a?.capabilities} runMode={runMode} wslDistro={wslDistro} onChange={(m, d) => set(() => ((runMode = m), (wslDistro = d)))} />
        <div mix={full}>
          <Field label="Purpose (optional)">
            {(id) => (
              <textarea id={id} value={purpose} rows={3} maxLength={4000} placeholder={PURPOSE_HELP} mix={on('input', (e) => set(() => (purpose = e.currentTarget.value)))} />
            )}
          </Field>
        </div>
        <SkillsField projectId={handle.props.projectId} role={role} changes={skills} onChange={(c) => set(() => (skills = c))} />
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
  // the mode the "acts without asking" box is ticked for, when the chosen template has such a mode (lib/permission.ts)
  let confirmedMode = '';

  const apply = async (templateId: string, mode: string) => {
    if (needsConfirmation(mode, confirmedMode)) return;
    note = '';
    const confirm = confirmationFor(mode, confirmedMode).confirmSkipsApprovals;
    const title = name.trim() || undefined;
    const projectId = handle.props.projectId;
    // the confirmation travels only when the mode needs it (and was ticked for): other applies are the call they always were
    const r = await act.run('apply', () => (confirm ? s.client.agentTemplateApply(templateId, projectId, title, true) : s.client.agentTemplateApply(templateId, projectId, title)));
    if (!r) return;
    note = appliedNote(r);
    name = '';
    confirmedMode = '';
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
    const tpl = templates.find((x) => x.id === chosen);
    const mode = tpl?.permissionMode ?? '';
    const { confirm: refusal, other: failure } = splitConfirmation(act.error);
    return (
      <section aria-label="Add an agent from a template" style={{ display: 'grid', gap: '12px' }}>
        <ErrorNote error={failure} what="Agent not created" />
        <div mix={templateRow}>
          <Field label="From a template">
            {(id) => (
              <Select
                id={id}
                label="Template"
                value={chosen}
                options={templates.map((x) => ({ value: x.id, label: `${x.name} (${x.role}, ${x.adapter})` }))}
                onChange={(v) => ((pick = v), (confirmedMode = ''), void handle.update())}
              />
            )}
          </Field>
          <Field label="Name (optional)">
            {(id) => (
              <input id={id} value={name} placeholder="the template's name" autocomplete="off" mix={on('input', (e) => ((name = e.currentTarget.value), void handle.update()))} />
            )}
          </Field>
          <Button
            variant="secondary"
            icon={LayoutTemplate}
            disabled={!!act.busy || ro.readOnly || needsConfirmation(mode, confirmedMode)}
            title={ro.readOnly ? ro.reason : undefined}
            mix={on('click', () => void apply(chosen, mode))}
          >
            Add to this team
          </Button>
        </div>
        <SkipApprovals mode={mode} confirmed={confirmedMode} onConfirm={(m) => ((confirmedMode = m), void handle.update())} who={tpl ? `agents made from ${tpl.name}` : 'agents made from this template'} error={refusal} />
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
  let runMode = a0.runMode ?? '';
  let wslDistro = a0.wslDistro ?? '';
  let skills: SkillChanges = noSkillChanges();
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
    if (skills.add.length > 0) u.addSkills = skills.add;
    if (skills.remove.length > 0) u.removeSkills = skills.remove;
    if (supportsWSL(handle.props.adapter?.capabilities) && (runMode !== (agent.runMode ?? '') || wslDistro !== (agent.wslDistro ?? ''))) {
      u.runMode = runMode;
      u.wslDistro = runMode === 'wsl' ? wslDistro : '';
    }
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
          {isNeedsConfirmation(act.error) ? (
            <Notice tone="warn" role="alert" testId="template-needs-confirmation" title="This agent cannot be saved as a template from here.">
              {act.error?.message} Make the template in the <a href={appRoutes.library.href()}>Library</a>, where you can confirm the permission mode.
            </Notice>
          ) : (
            <ErrorNote error={act.error} what="Agent not changed" />
          )}
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
        <RunModeField caps={adapter?.capabilities} runMode={runMode} wslDistro={wslDistro} onChange={(m, d) => set(() => ((runMode = m), (wslDistro = d)))} />
        <div mix={full}>
          <Field label="Purpose">
            {(id) => (
              <textarea id={id} value={purpose} rows={4} maxLength={4000} placeholder={PURPOSE_HELP} mix={on('input', (e) => set(() => (purpose = e.currentTarget.value)))} />
            )}
          </Field>
        </div>
        <ClaudePracticesPanel info={info} on={practices} onChange={(v) => set(() => (practices = v))} />
        <RouteModelsField routes={routes} onRoutes={(r) => set(() => (routes = r))} suggestions={adapter?.capabilities?.models ?? []} />
        <SkillsField projectId={agent.projectId} agentId={agent.id} role={role} changes={skills} onChange={(c) => set(() => (skills = c))} />
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
