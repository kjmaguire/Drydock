// Define a team member once: the CLI, model, effort, role, purpose, the library servers it should get and the registry skills
// it brings. The Library screen shows this form for a new template and for editing one.

import { css, on, type Handle } from 'remix/component';
import { Cpu, Server } from 'lucide';
import type { AgentTemplate, SkillChoice } from '../../api/libraryClient.ts';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import type { AdapterInfo, SkillRef } from '../../api/types.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, query, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
import { Select, selectValues } from '../../ui/select.tsx';
import { t } from '../../ui/tokens.ts';
import { ClaudePracticesPanel, ModelEffortFields, claudePractices } from './fields.tsx';
import { PURPOSE_HELP, sortAdapters } from './team.ts';

export interface TemplateFormProps {
  initial?: AgentTemplate;
  adapters: readonly AdapterInfo[];
  servers: readonly McpLibraryServer[];
  onDone: (saved: AgentTemplate) => void;
  onCancel: () => void;
}

const full = css({ gridColumn: '1 / -1' });
const serversCss = css({
  display: 'grid',
  gap: t.s2,
  gridColumn: '1 / -1',
  '& legend': { display: 'inline-flex', alignItems: 'center', gap: '6px' },
  '& label': { display: 'flex', alignItems: 'center', gap: t.s2, fontSize: t.textMd, cursor: 'pointer', minWidth: '0' },
  '& label input': { margin: '0' },
  '& p': { margin: '0', fontSize: t.textSm },
});

const skillRows = css({
  display: 'grid',
  gap: t.s1,
  margin: '0',
  padding: '0',
  listStyle: 'none',
  '& li': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', minWidth: '0', fontSize: t.textMd },
  '& li span': { color: t.textMuted, fontSize: t.textSm },
  '& li button': { marginLeft: 'auto' },
});
const addRow = css({ display: 'flex', alignItems: 'end', gap: t.s2, flexWrap: 'wrap', '& > :first-child': { flex: '1', minWidth: '220px' } });

const keyOf = (r: SkillRef): string => `${r.registry}/${r.name}@${r.ref}`;

/** One option line for a skill a template can carry: its name, its registry and where Drydock knows it from. */
export const choiceText = (c: SkillChoice): string => `${c.name} (${c.registry})${c.from.length ? `, from ${c.from.join(', ')}` : ''}`;

export function TemplateForm(handle: Handle<TemplateFormProps>) {
  const s = services(handle);
  watch(handle, s.workspace);
  const act = action(handle);
  const init = handle.props.initial;
  let name = init?.name ?? '';
  let role = init?.role ?? '';
  let purpose = init?.purpose ?? '';
  // the adapter list arrives after the form opens, so the first one is the default until the human picks
  let picked = init?.adapter ?? '';
  let model = init?.model ?? '';
  let effort = init?.effort ?? '';
  let mode = init?.permissionMode ?? '';
  let practices = init?.practices !== false;
  let servers: string[] = init?.mcpServers ?? [];
  let skills: SkillRef[] = init?.skills ?? [];
  let pick = '';
  const set = (f: () => void) => {
    f();
    void handle.update();
  };
  const choices = query(handle, { load: () => s.client.agentTemplateSkillChoices() });
  // a template carries one entry per skill name (the core keeps the first)
  const available = (): SkillChoice[] => (choices.data ?? []).filter((c) => !skills.some((k) => k.name === c.name));
  const addSkill = () => {
    const list = available();
    const c = list.find((x) => keyOf(x) === pick) ?? list[0];
    if (c) set(() => ((skills = [...skills, { registry: c.registry, name: c.name, ref: c.ref }]), (pick = '')));
  };
  const adapters = () => sortAdapters(handle.props.adapters); // ready adapters first, as on the Team page
  const adapterId = () => picked || adapters()[0]?.id || '';
  const pr = claudePractices(handle, {
    adapter: adapterId,
    role: () => role,
    fields: { model: () => model, effort: () => effort, set: (m, e) => set(() => ((model = m), (effort = e))) },
  });

  const submit = async () => {
    const initial = handle.props.initial;
    const adapter = adapterId();
    const caps = adapters().find((a) => a.id === adapter)?.capabilities;
    const r = await act.run('save', () =>
      s.client.agentTemplateSave({
        ...(initial ? { id: initial.id } : {}),
        name: name.trim(),
        role: role.trim(),
        purpose: purpose.trim(),
        adapter,
        model: caps?.modelSelection === true ? model.trim() : '',
        effort: caps?.effortControl === true ? effort.trim() : '',
        permissionMode: mode,
        providerId: initial?.providerId ?? '',
        mcpServers: servers,
        skills,
        ...(adapter === 'claude' ? { practices } : {}),
      }),
    );
    if (r) handle.props.onDone(r);
  };

  return () => {
    const { initial, servers: library, onCancel } = handle.props;
    const ro = s.workspace.license;
    const list = adapters();
    const adapter = adapterId();
    const chosen = list.find((a) => a.id === adapter);
    const modes = chosen?.capabilities?.permissionModes ?? [];
    const info = pr.info;
    return (
      <form
        mix={[formGrid, on('submit', (e) => (e.preventDefault(), void submit()))]}
        aria-label={initial ? `Edit ${initial.name}` : 'New agent template'}
        data-testid="template-form"
      >
        <div mix={full}>
          <ErrorNote error={act.error} what="Template not saved" />
          <ReadOnlyNote />
        </div>
        <Field label="Template name">
          {(id) => <input id={id} value={name} required autocomplete="off" mix={on('input', (e) => set(() => (name = e.currentTarget.value)))} />}
        </Field>
        <Field label="Role">
          {(id) => <input id={id} value={role} required autocomplete="off" mix={on('input', (e) => set(() => (role = e.currentTarget.value)))} />}
        </Field>
        <Field label="Adapter">
          {(id) => (
            <Select
              id={id}
              label="Adapter"
              value={adapter}
              options={list.map((a) => ({ value: a.id, label: a.id }))}
              placeholder="Choose an agent CLI"
              onChange={(v) => set(() => ((picked = v), (model = ''), (effort = ''), (mode = '')))}
            />
          )}
        </Field>
        <ModelEffortFields caps={chosen?.capabilities} model={model} effort={effort} onModel={(v) => set(() => (model = v))} onEffort={(v) => set(() => (effort = v))} />
        {modes.length > 0 ? (
          <Field label="Permission mode">
            {(id) => (
              <Select
                id={id}
                label="Permission mode"
                value={mode}
                options={[{ value: '', label: 'default for background agents (acceptEdits)' }, ...selectValues(modes)]}
                onChange={(v) => set(() => (mode = v))}
              />
            )}
          </Field>
        ) : null}
        <div mix={full}>
          <Field label="Purpose (optional)">
            {(id) => (
              <textarea id={id} value={purpose} rows={3} maxLength={4000} placeholder={PURPOSE_HELP} mix={on('input', (e) => set(() => (purpose = e.currentTarget.value)))} />
            )}
          </Field>
        </div>
        <ClaudePracticesPanel info={info} on={practices} onChange={(v) => set(() => (practices = v))} />
        <fieldset mix={serversCss}>
          <legend>
            <Icon icon={Server} size={14} />
            MCP servers it gets when it is put into a project
          </legend>
          {library.length === 0 ? <p mix={muted}>No servers are connected yet.</p> : null}
          {library.map((srv) => (
            <label key={srv.id}>
              <input
                type="checkbox"
                aria-label={`Template gets ${srv.display}`}
                checked={servers.includes(srv.id)}
                mix={on('change', (e) => {
                  const on = e.currentTarget.checked;
                  set(() => (servers = on ? [...servers, srv.id] : servers.filter((x) => x !== srv.id)));
                })}
              />
              <span>{srv.display}</span> <code>{srv.id}</code>
            </label>
          ))}
        </fieldset>
        <fieldset mix={serversCss} data-testid="template-skills">
          <legend>
            <Icon icon={Cpu} size={14} />
            Skills it brings into a project
          </legend>
          <p mix={muted}>
            Registry skills pinned to a commit. Putting the template into a project installs them there and gives them to the new agent; a skill the
            whole team already has stays with the team.
          </p>
          {skills.length === 0 ? <p mix={muted}>No skills.</p> : null}
          {skills.length > 0 ? (
            <ul mix={skillRows} aria-label="Skills the template brings">
              {skills.map((k) => (
                <li key={k.name}>
                  <code>{k.name}</code>
                  <span>
                    {k.registry}, {k.ref ? `pinned at ${k.ref.slice(0, 12)}` : 'the newest commit when it is put into a project'}
                  </span>
                  <Button size="sm" variant="ghost" aria-label={`Remove ${k.name}`} mix={on('click', () => set(() => (skills = skills.filter((x) => x.name !== k.name))))}>
                    Remove
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
          <ErrorNote error={choices.error} what="Skills to add" />
          {available().length > 0 ? (
            <div mix={addRow}>
              <Field label="Add a skill">
                {(id) => (
                  <Select
                    id={id}
                    value={pick || keyOf(available()[0]!)}
                    options={available().map((c) => ({ value: keyOf(c), label: choiceText(c) }))}
                    onChange={(v) => set(() => (pick = v))}
                  />
                )}
              </Field>
              <Button size="sm" mix={on('click', () => addSkill())}>
                Add skill
              </Button>
            </div>
          ) : choices.data ? (
            <p mix={muted}>No other skill to add: import one on a project's Skills page, or put in a prebuilt agent that comes with skills.</p>
          ) : null}
        </fieldset>
        <div mix={formActions}>
          <Button type="submit" variant="primary" disabled={!!act.busy || ro.readOnly || !name.trim() || !role.trim() || !adapter} title={ro.readOnly ? ro.reason : undefined}>
            {act.busy === 'save' ? 'Saving...' : initial ? 'Save template' : 'Create template'}
          </Button>
          <Button mix={on('click', () => onCancel())}>Cancel</Button>
        </div>
      </form>
    );
  };
}
