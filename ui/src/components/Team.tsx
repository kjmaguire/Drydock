import { useState, type FormEvent } from 'react';
import type { AdapterInfo, Agent, Capabilities, PresenceState } from '../api/types';
import { useClient } from '../state/context';
import { useAdapters } from '../state/hooks';
import { Badge, ErrorNote, Page, PresenceDot } from './common';

const CAP_LABEL: [keyof Capabilities, string][] = [
  ['streaming', 'Streaming'],
  ['structuredOutput', 'Structured output'],
  ['modelSelection', 'Model selection'],
  ['effortControl', 'Effort control'],
  ['sessionResume', 'Session resume'],
  ['interrupt', 'Interrupt'],
  ['mcp', 'MCP'],
  ['tokenUsage', 'Token usage'],
  ['costReporting', 'Cost reporting'],
  ['headless', 'Headless'],
  ['imageInput', 'Image input'],
  ['toolCallVisibility', 'Tool-call visibility'],
];

/** Capabilities are shown as verified or "not verified": a missing or false capability is never claimed as absent. */
export function CapabilityList({ caps }: { caps: Capabilities | undefined }) {
  return (
    <ul className="caps" aria-label="Capabilities">
      {CAP_LABEL.map(([k, label]) => {
        const ok = caps?.[k] === true;
        return (
          <li key={k} data-cap={k} data-verified={ok}>
            <span aria-hidden="true">{ok ? '\u2714' : '\u2013'}</span> {label}: <span className={ok ? 'ok' : 'muted'}>{ok ? 'verified' : 'not verified'}</span>
          </li>
        );
      })}
    </ul>
  );
}

function AdapterCard({ a }: { a: AdapterInfo }) {
  const d = a.detection;
  return (
    <article className="card" data-testid="adapter" data-adapter={a.id}>
      <header className="card-head">
        <h3>{a.id}</h3>
        <Badge tone={d.status === 'ready' ? 'ok' : 'warn'}>{d.status}</Badge>
        <span className="muted">{d.installed ? 'installed' : 'not installed'}</span>
      </header>
      <dl className="kv">
        {d.version ? (
          <>
            <dt>Version</dt>
            <dd>{d.version}</dd>
          </>
        ) : null}
        {d.path ? (
          <>
            <dt>Path</dt>
            <dd>
              <code>{d.path}</code>
            </dd>
          </>
        ) : null}
        <dt>Signed in</dt>
        <dd>{d.authenticated === true ? 'yes' : d.authenticated === false ? 'no' : 'not reported'}</dd>
      </dl>
      {d.authDetail ? <p className="muted">{d.authDetail}</p> : null}
      {a.error ? <p className="error-note">{a.error}</p> : null}
      {(d.problems ?? []).map((p, i) => (
        <p key={i} className="error-note">
          {p}
        </p>
      ))}
      {d.installHint ? (
        <p className="muted">
          Install guidance (Drydock never runs this for you): <code>{d.installHint}</code>
        </p>
      ) : null}
      <details>
        <summary>Capabilities (absent means not verified)</summary>
        <CapabilityList caps={a.capabilities} />
      </details>
    </article>
  );
}

/** adapter.list comes back in map order; show ready adapters first, then by id. */
export function sortAdapters(list: readonly AdapterInfo[]): AdapterInfo[] {
  const rank = (a: AdapterInfo) => (a.detection.status === 'ready' ? 0 : a.detection.installed ? 1 : 2);
  return [...list].sort((a, b) => rank(a) - rank(b) || a.id.localeCompare(b.id));
}

export function TeamView({ projectId, agents, presence, onCreated }: { projectId: string; agents: readonly Agent[]; presence: readonly PresenceState[]; onCreated: () => void }) {
  const adapters = useAdapters();
  const sorted = sortAdapters(adapters.data ?? []);
  return (
    <Page title="Team">
      <h2>Agents</h2>
      {agents.length === 0 ? <p className="muted">No agents yet. A task needs a worker and a different agent as reviewer.</p> : null}
      <ul className="agent-list" aria-label="Agents">
        {agents.map((a) => (
          <li key={a.id} data-testid="agent" data-name={a.name}>
            <PresenceDot presence={presence.find((p) => p.agentId === a.id)} />
            <strong>{a.name}</strong> <span className="muted">{a.role}</span> <code>{a.adapter}</code>
            {a.model ? <span className="muted"> model {a.model}</span> : null}
            {a.effort ? <span className="muted"> effort {a.effort}</span> : null}
          </li>
        ))}
      </ul>
      <h2>Add an agent</h2>
      <AgentForm projectId={projectId} adapters={sorted} onCreated={onCreated} />
      <h2>Adapters</h2>
      <ErrorNote error={adapters.error} what="Could not list adapters" />
      {adapters.loading && !adapters.data ? <p className="muted">Detecting...</p> : null}
      <div className="cards">
        {sorted.map((a) => (
          <AdapterCard key={a.id} a={a} />
        ))}
      </div>
    </Page>
  );
}

const ROLE_SUGGESTIONS = ['backend', 'frontend', 'reviewer', 'tester', 'architect'];

function AgentForm({ projectId, adapters, onCreated }: { projectId: string; adapters: AdapterInfo[]; onCreated: () => void }) {
  const client = useClient();
  const [name, setName] = useState('');
  const [role, setRole] = useState('');
  const [adapter, setAdapter] = useState('');
  const [model, setModel] = useState('');
  const [effort, setEffort] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | undefined>();
  const chosen = adapters.find((a) => a.id === (adapter || adapters[0]?.id));
  const adapterId = chosen?.id ?? '';
  const canModel = chosen?.capabilities?.modelSelection === true;
  const canEffort = chosen?.capabilities?.effortControl === true;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await client.agentCreate({ projectId, name: name.trim(), role: role.trim(), adapter: adapterId, model: canModel ? model.trim() : '', effort: canEffort ? effort.trim() : '' });
      setName('');
      setRole('');
      setModel('');
      setEffort('');
      onCreated();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy(false);
    }
  };
  return (
    <form className="form form-grid" onSubmit={submit}>
      <ErrorNote error={error} what="Agent not created" />
      <label>
        Agent name
        <input value={name} onChange={(e) => setName(e.target.value)} required />
      </label>
      <label>
        Role
        <input value={role} onChange={(e) => setRole(e.target.value)} list="role-suggestions" required />
        <datalist id="role-suggestions">
          {ROLE_SUGGESTIONS.map((r) => (
            <option key={r} value={r} />
          ))}
        </datalist>
      </label>
      <label>
        Adapter
        <select aria-label="Adapter" value={adapterId} onChange={(e) => setAdapter(e.target.value)} required>
          {adapters.map((a) => (
            <option key={a.id} value={a.id}>
              {a.id} ({a.detection.installed ? a.detection.status : 'not installed'})
            </option>
          ))}
        </select>
      </label>
      <label>
        Model
        <input value={model} onChange={(e) => setModel(e.target.value)} disabled={!canModel} placeholder={canModel ? 'adapter default' : 'model selection not verified for this adapter'} />
      </label>
      <label>
        Effort
        <input value={effort} onChange={(e) => setEffort(e.target.value)} disabled={!canEffort} placeholder={canEffort ? 'adapter default' : 'effort control not verified for this adapter'} />
      </label>
      <div className="form-actions">
        <button type="submit" className="primary" disabled={busy || !name.trim() || !role.trim() || !adapterId}>
          Create agent
        </button>
      </div>
    </form>
  );
}
