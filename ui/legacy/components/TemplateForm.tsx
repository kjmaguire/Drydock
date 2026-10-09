import { useState, type FormEvent } from 'react';
import type { AgentTemplate } from '../api/libraryClient';
import type { McpLibraryServer } from '../api/mcpClient';
import type { AdapterInfo } from '../api/types';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { ReadOnlyNote, useReadOnly } from '../state/license';
import { ErrorNote } from './common';
import { ClaudePracticesPanel, useClaudePractices } from './ClaudePractices';
import { ModelEffortFields, PURPOSE_HELP, sortAdapters } from './Team';

/** Define a team member once: the CLI, model, effort, role, purpose and the library servers it should get. */
export function TemplateForm({
  initial,
  adapters: unsorted,
  servers,
  onDone,
  onCancel,
}: {
  initial?: AgentTemplate;
  adapters: readonly AdapterInfo[];
  servers: readonly McpLibraryServer[];
  onDone: (saved: AgentTemplate) => void;
  onCancel: () => void;
}) {
  const client = useClient();
  const ro = useReadOnly();
  const act = useAction();
  const adapters = sortAdapters(unsorted); // ready adapters first, as on the Team page
  const [name, setName] = useState(initial?.name ?? '');
  const [role, setRole] = useState(initial?.role ?? '');
  const [purpose, setPurpose] = useState(initial?.purpose ?? '');
  // the adapter list arrives after the form opens, so the first one is the default until the human picks
  const [picked0, setAdapter] = useState(initial?.adapter ?? '');
  const adapter = picked0 || adapters[0]?.id || '';
  const [model, setModel] = useState(initial?.model ?? '');
  const [effort, setEffort] = useState(initial?.effort ?? '');
  const [mode, setMode] = useState(initial?.permissionMode ?? '');
  const [practices, setPractices] = useState(initial?.practices !== false);
  const [picked, setPicked] = useState<string[]>(initial?.mcpServers ?? []);
  const chosen = adapters.find((a) => a.id === adapter);
  const canModel = chosen?.capabilities?.modelSelection === true;
  const canEffort = chosen?.capabilities?.effortControl === true;
  const modes = chosen?.capabilities?.permissionModes ?? [];
  const info = useClaudePractices(adapter, role, model, effort, setModel, setEffort);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const r = await act.run('save', () =>
      client.agentTemplateSave({
        ...(initial ? { id: initial.id } : {}),
        name: name.trim(), role: role.trim(), purpose: purpose.trim(), adapter, model: canModel ? model.trim() : '', effort: canEffort ? effort.trim() : '',
        permissionMode: mode, providerId: initial?.providerId ?? '', mcpServers: picked, practices: adapter === 'claude' ? practices : undefined,
      }),
    );
    if (r) onDone(r);
  };

  return (
    <form className="form form-grid" onSubmit={submit} aria-label={initial ? `Edit ${initial.name}` : 'New agent template'} data-testid="template-form">
      <ErrorNote error={act.error} what="Template not saved" />
      <ReadOnlyNote />
      <label>
        Template name
        <input value={name} onChange={(e) => setName(e.target.value)} required />
      </label>
      <label>
        Role
        <input value={role} onChange={(e) => setRole(e.target.value)} required />
      </label>
      <label>
        Purpose (optional)
        <textarea value={purpose} onChange={(e) => setPurpose(e.target.value)} rows={3} maxLength={4000} placeholder={PURPOSE_HELP} />
      </label>
      <label>
        Adapter
        <select
          aria-label="Adapter"
          value={adapter}
          onChange={(e) => {
            setAdapter(e.target.value);
            setModel('');
            setEffort('');
            setMode('');
          }}
          required
        >
          {adapters.map((a) => (
            <option key={a.id} value={a.id}>
              {a.id}
            </option>
          ))}
        </select>
      </label>
      <ModelEffortFields caps={chosen?.capabilities} model={model} effort={effort} setModel={setModel} setEffort={setEffort} />
      <ClaudePracticesPanel info={info} on={practices} onChange={setPractices} />
      {modes.length > 0 ? (
        <label>
          Permission mode
          <select aria-label="Permission mode" value={mode} onChange={(e) => setMode(e.target.value)}>
            <option value="">default for background agents (acceptEdits)</option>
            {modes.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </label>
      ) : null}
      <fieldset>
        <legend>MCP servers it gets when it is put into a project</legend>
        {servers.length === 0 ? <p className="muted">No servers are connected yet.</p> : null}
        {servers.map((s) => (
          <label key={s.id} className="inline">
            <input type="checkbox" aria-label={`Template gets ${s.display}`} checked={picked.includes(s.id)} onChange={(e) => setPicked(e.target.checked ? [...picked, s.id] : picked.filter((x) => x !== s.id))} /> {s.display}{' '}
            <code>{s.id}</code>
          </label>
        ))}
      </fieldset>
      <div className="form-actions">
        <button type="submit" className="primary" disabled={act.busy !== '' || ro.readOnly || !name.trim() || !role.trim() || !adapter} title={ro.readOnly ? ro.reason : undefined}>
          {act.busy === 'save' ? 'Saving...' : initial ? 'Save template' : 'Create template'}
        </button>
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
