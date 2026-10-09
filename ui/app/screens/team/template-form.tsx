// Define a team member once: the CLI, model, effort, role, purpose and the library servers it should get. The Library
// screen shows this form for a new template and for editing one.

import { css, on, type Handle } from 'remix/component';
import { Server } from 'lucide';
import type { AgentTemplate } from '../../api/libraryClient.ts';
import type { McpLibraryServer } from '../../api/mcpClient.ts';
import type { AdapterInfo } from '../../api/types.ts';
import { ReadOnlyNote } from '../../shell/license-banner.tsx';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field, formActions, formGrid } from '../../ui/form.tsx';
import { Icon } from '../../ui/icon.tsx';
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
  const set = (f: () => void) => {
    f();
    void handle.update();
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
            <select
              id={id}
              aria-label="Adapter"
              value={adapter}
              required
              mix={on('change', (e) => set(() => ((picked = e.currentTarget.value), (model = ''), (effort = ''), (mode = ''))))}
            >
              {list.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.id}
                </option>
              ))}
            </select>
          )}
        </Field>
        <ModelEffortFields caps={chosen?.capabilities} model={model} effort={effort} onModel={(v) => set(() => (model = v))} onEffort={(v) => set(() => (effort = v))} />
        {modes.length > 0 ? (
          <Field label="Permission mode">
            {(id) => (
              <select id={id} aria-label="Permission mode" value={mode} mix={on('change', (e) => set(() => (mode = e.currentTarget.value)))}>
                <option value="">default for background agents (acceptEdits)</option>
                {modes.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
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
