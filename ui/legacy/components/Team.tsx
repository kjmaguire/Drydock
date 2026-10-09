import { useState, type FormEvent } from 'react';
import type { RouteModel, AdapterInfo, Agent, Capabilities, PresenceState } from '../api/types';
import { useId } from 'react';
import { useClient } from '../state/context';
import { useAdapters, useLive } from '../state/hooks';
import { Badge, ErrorNote, Page, PresenceDot } from './common';
import { ReadOnlyNote, useReadOnly } from '../state/license';
import { ClaudePracticesPanel, useClaudePractices } from './ClaudePractices';

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

export function TeamView({
  projectId,
  agents,
  presence,
  onCreated,
  onInspect,
  onMessage,
}: {
  projectId: string;
  agents: readonly Agent[];
  presence: readonly PresenceState[];
  onCreated: () => void;
  onInspect?: (agentId: string) => void;
  onMessage?: (agentId: string) => void;
}) {
  const adapters = useAdapters();
  const sorted = sortAdapters(adapters.data ?? []);
  const [editing, setEditing] = useState('');
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
            {onInspect ? (
              <button className="link-button" onClick={() => onInspect(a.id)}>
                Inspect
              </button>
            ) : null}
            {onMessage ? (
              <button className="link-button" onClick={() => onMessage(a.id)}>
                Message
              </button>
            ) : null}
            <button className="link-button" onClick={() => setEditing(editing === a.id ? '' : a.id)} aria-expanded={editing === a.id} aria-label={`Edit ${a.name}`}>
              Edit
            </button>
            {a.purpose ? <div className="muted" data-testid="agent-purpose">Purpose: {a.purpose.length > 160 ? `${a.purpose.slice(0, 160)}...` : a.purpose}</div> : null}
            {editing === a.id ? (
              <AgentEditor
                agent={a}
                adapter={sorted.find((x) => x.id === a.adapter)}
                onDone={() => {
                  setEditing('');
                  onCreated();
                }}
                onCancel={() => setEditing('')}
              />
            ) : null}
          </li>
        ))}
      </ul>
      <h2>Add an agent</h2>
      <FromTemplate projectId={projectId} onCreated={onCreated} />
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

/** Drop a saved team member into this project: an ordinary agent is made from the template, with the MCP servers it lists. */
function FromTemplate({ projectId, onCreated }: { projectId: string; onCreated: () => void }) {
  const client = useClient();
  const ro = useReadOnly();
  const q = useLive(() => client.agentTemplateList(), [], (e) => e.type.startsWith('agenttemplate.'));
  const [pick, setPick] = useState('');
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | undefined>();
  const [note, setNote] = useState('');
  const templates = q.data ?? [];
  if (templates.length === 0) {
    return (
      <p className="muted">
        Team members you save as templates in the <a href="#/library">Library</a> can be added here with one click.
      </p>
    );
  }
  const chosen = pick || templates[0]!.id;
  return (
    <section aria-label="Add an agent from a template">
      <ErrorNote error={error} what="Agent not created" />
      <div className="form-actions">
        <label>
          From a template
          <select aria-label="Template" value={chosen} onChange={(e) => setPick(e.target.value)}>
            {templates.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name} ({t.role}, {t.adapter})
              </option>
            ))}
          </select>
        </label>
        <label>
          Name (optional)
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="the template's name" />
        </label>
        <button
          className="primary"
          disabled={busy || ro.readOnly}
          title={ro.readOnly ? ro.reason : undefined}
          onClick={async () => {
            setBusy(true);
            setError(undefined);
            setNote('');
            try {
              const r = await client.agentTemplateApply(chosen, projectId, name.trim() || undefined);
              setNote(`${r.agent.name} joined the team${r.granted.length ? `, with ${r.granted.join(', ')}` : ''}${r.skipped.length ? `. Not granted: ${r.skipped.join('; ')}` : ''}.`);
              setName('');
              onCreated();
            } catch (err) {
              setError(err as Error);
            } finally {
              setBusy(false);
            }
          }}
        >
          Add to this team
        </button>
      </div>
      {note ? <p role="status">{note}</p> : null}
    </section>
  );
}

const ROLE_SUGGESTIONS = ['backend', 'frontend', 'reviewer', 'tester', 'architect'];

/** Model and effort inputs. Effort is a list when the adapter is known to accept only certain levels (a wrong one would fail the
 *  run); model is free text with the adapter's aliases offered, because full model names are accepted too. */
export function ModelEffortFields({ caps, model, effort, setModel, setEffort }: { caps: Capabilities | undefined; model: string; effort: string; setModel: (v: string) => void; setEffort: (v: string) => void }) {
  const listId = useId();
  const canModel = caps?.modelSelection === true;
  const canEffort = caps?.effortControl === true;
  const efforts = caps?.efforts ?? [];
  const models = caps?.models ?? [];
  return (
    <>
      <label>
        Model
        <input value={model} onChange={(e) => setModel(e.target.value)} disabled={!canModel} list={models.length > 0 ? listId : undefined} spellCheck={false} placeholder={canModel ? 'adapter default' : 'model selection not verified for this adapter'} />
        {models.length > 0 ? (
          <datalist id={listId}>
            {models.map((m) => (
              <option key={m} value={m} />
            ))}
          </datalist>
        ) : null}
      </label>
      <label>
        Effort
        {efforts.length > 0 ? (
          <select aria-label="Effort" value={effort} onChange={(e) => setEffort(e.target.value)} disabled={!canEffort}>
            <option value="">adapter default</option>
            {efforts.map((x) => (
              <option key={x} value={x}>
                {x}
              </option>
            ))}
            {effort && !efforts.includes(effort) ? <option value={effort}>{effort} (not accepted)</option> : null}
          </select>
        ) : (
          <input aria-label="Effort" value={effort} onChange={(e) => setEffort(e.target.value)} disabled={!canEffort} spellCheck={false} placeholder={canEffort ? 'adapter default' : 'effort control not verified for this adapter'} />
        )}
      </label>
    </>
  );
}

export const PURPOSE_HELP = 'Standing instructions for this agent, sent with every run: what it owns, what it must not touch, how it should work. It never overrides the task, the independent review or your approval.';

function AgentForm({ projectId, adapters, onCreated }: { projectId: string; adapters: AdapterInfo[]; onCreated: () => void }) {
  const client = useClient();
  const [name, setName] = useState('');
  const [role, setRole] = useState('');
  const [purpose, setPurpose] = useState('');
  const [adapter, setAdapter] = useState('');
  const [model, setModel] = useState('');
  const [effort, setEffort] = useState('');
  const [practices, setPractices] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | undefined>();
  const ro = useReadOnly();
  const chosen = adapters.find((a) => a.id === (adapter || adapters[0]?.id));
  const adapterId = chosen?.id ?? '';
  const canModel = chosen?.capabilities?.modelSelection === true;
  const canEffort = chosen?.capabilities?.effortControl === true;
  const info = useClaudePractices(adapterId, role, model, effort, setModel, setEffort);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await client.agentCreate({ projectId, name: name.trim(), role: role.trim(), purpose: purpose.trim(), adapter: adapterId, model: canModel ? model.trim() : '', effort: canEffort ? effort.trim() : '', practices: adapterId === 'claude' ? practices : undefined });
      setName('');
      setRole('');
      setPurpose('');
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
      <ReadOnlyNote />
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
        Purpose (optional)
        <textarea value={purpose} onChange={(e) => setPurpose(e.target.value)} rows={3} maxLength={4000} placeholder={PURPOSE_HELP} />
      </label>
      <label>
        Adapter
        <select aria-label="Adapter" value={adapterId} onChange={(e) => { setAdapter(e.target.value); setModel(''); setEffort(''); }} required>
          {adapters.map((a) => (
            <option key={a.id} value={a.id}>
              {a.id} ({a.detection.installed ? a.detection.status : 'not installed'})
            </option>
          ))}
        </select>
      </label>
      <ModelEffortFields caps={chosen?.capabilities} model={model} effort={effort} setModel={setModel} setEffort={setEffort} />
      <ClaudePracticesPanel info={info} on={practices} onChange={setPractices} />
      <div className="form-actions">
        <button type="submit" className="primary" disabled={busy || ro.readOnly || !name.trim() || !role.trim() || !adapterId} title={ro.readOnly ? ro.reason : undefined}>
          Create agent
        </button>
      </div>
    </form>
  );
}

/** Edit an agent in place. The adapter is fixed; a changed model, effort or purpose applies to the agent's next session. */
function AgentEditor({ agent, adapter, onDone, onCancel }: { agent: Agent; adapter: AdapterInfo | undefined; onDone: () => void; onCancel: () => void }) {
  const client = useClient();
  const ro = useReadOnly();
  const [name, setName] = useState(agent.name);
  const [role, setRole] = useState(agent.role);
  const [purpose, setPurpose] = useState(agent.purpose ?? '');
  const [model, setModel] = useState(agent.model);
  const [effort, setEffort] = useState(agent.effort);
  const [practices, setPractices] = useState(agent.practices !== false);
  const [routes, setRoutes] = useState<RouteModel[]>(agent.routeModels ?? []);
  const [busy, setBusy] = useState('');
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [savedAs, setSavedAs] = useState('');
  const [error, setError] = useState<Error | undefined>();
  // an existing agent keeps its model and effort: the recommendation is shown, not applied over them
  const info = useClaudePractices(agent.adapter, role, 'keep', 'keep', () => undefined, () => undefined);
  const run = async (what: string, f: () => Promise<unknown>) => {
    setBusy(what);
    setError(undefined);
    try {
      await f();
      onDone();
    } catch (err) {
      setError(err as Error);
    } finally {
      setBusy('');
    }
  };
  const save = (e: FormEvent) => {
    e.preventDefault();
    return run('save', () => client.agentUpdate({ id: agent.id, name: name.trim(), role: role.trim(), purpose: purpose.trim(), model: model.trim(), effort: effort.trim(), practices: agent.adapter === 'claude' ? practices : undefined, routeModels: JSON.stringify(routes) === JSON.stringify(agent.routeModels ?? []) ? undefined : routes.filter((r) => r.model.trim()) }));
  };
  return (
    <form className="form form-grid" onSubmit={save} aria-label={`Edit ${agent.name}`} data-testid="agent-editor">
      <ErrorNote error={error} what="Agent not changed" />
      {savedAs ? (
        <p role="status" data-testid="saved-as-template">
          Saved as the template {savedAs}. Find it in the <a href="#/library">Library</a>.
        </p>
      ) : null}
      <ReadOnlyNote />
      <p className="muted">
        Adapter <code>{agent.adapter}</code> cannot change: add a new agent to use a different CLI. Changes apply from the agent&apos;s next session.
      </p>
      <label>
        Agent name
        <input value={name} onChange={(e) => setName(e.target.value)} required />
      </label>
      <label>
        Role
        <input value={role} onChange={(e) => setRole(e.target.value)} list="role-suggestions" required />
      </label>
      <label>
        Purpose
        <textarea value={purpose} onChange={(e) => setPurpose(e.target.value)} rows={4} maxLength={4000} placeholder={PURPOSE_HELP} />
      </label>
      <ModelEffortFields caps={adapter?.capabilities} model={model} effort={effort} setModel={setModel} setEffort={setEffort} />
      <ClaudePracticesPanel info={info} on={practices} onChange={setPractices} />
      <RouteModelsField routes={routes} setRoutes={setRoutes} suggestions={adapter?.capabilities?.models ?? []} />
      <div className="form-actions">
        <button type="submit" className="primary" disabled={busy !== '' || ro.readOnly || !name.trim() || !role.trim()} title={ro.readOnly ? ro.reason : undefined}>
          {busy === 'save' ? 'Saving...' : 'Save changes'}
        </button>
        <button type="button" onClick={onCancel} disabled={busy !== ''}>
          Cancel
        </button>
        <button
          type="button"
          disabled={busy !== ''}
          onClick={async () => {
            setBusy('template');
            setError(undefined);
            try {
              const t = await client.agentTemplateFromAgent(agent.id);
              setSavedAs(t.name);
            } catch (err) {
              setError(err as Error);
            } finally {
              setBusy('');
            }
          }}
        >
          Save as a template
        </button>
        {confirmRemove ? (
          <>
            <span>Remove {agent.name} from the team? Its history stays.</span>
            <button type="button" className="danger" disabled={busy !== ''} onClick={() => run('remove', () => client.agentArchive(agent.id))}>
              {busy === 'remove' ? 'Removing...' : 'Yes, remove'}
            </button>
            <button type="button" onClick={() => setConfirmRemove(false)} disabled={busy !== ''}>
              Keep
            </button>
          </>
        ) : (
          <button type="button" onClick={() => setConfirmRemove(true)} disabled={busy !== ''}>
            Remove from team
          </button>
        )}
      </div>
    </form>
  );
}

/**
 * The models the router may choose between for this agent's turns (internal/core/turnrouting.go). Off (empty) by default:
 * the agent then always runs its model. With a list the router starts on a cheap model and escalates to a premium one when
 * the work gets stuck, always on this agent's own CLI.
 */
function RouteModelsField({ routes, setRoutes, suggestions }: { routes: RouteModel[]; setRoutes: (r: RouteModel[]) => void; suggestions: string[] }) {
  const on = routes.length > 0;
  return (
    <fieldset className="route-models" data-testid="route-models">
      <legend>Model routing</legend>
      <label className="inline">
        <input
          type="checkbox"
          checked={on}
          onChange={(e) =>
            setRoutes(
              e.target.checked
                ? suggestions.includes('haiku') && suggestions.includes('opus')
                  ? [
                      { model: 'haiku', tier: 'cheap' },
                      { model: 'opus', tier: 'premium' },
                    ]
                  : [
                      { model: '', tier: 'cheap' },
                      { model: '', tier: 'premium' },
                    ]
                : [],
            )
          }
        />{' '}
        Let the router choose this agent&apos;s model for each turn
      </label>
      {on ? (
        <>
          <p className="muted">It starts on a cheap model and moves to a premium one after three cheap tries that fail their checks, or when the reviewer keeps sending premium work back. It only chooses from this list, on this agent&apos;s own CLI. Every choice and its reason is in the task&apos;s run explanation.</p>
          {routes.map((r, i) => (
            <div key={i} className="route-row">
              <input aria-label={`Routed model ${i + 1}`} value={r.model} list="route-model-suggestions" onChange={(e) => setRoutes(routes.map((x, j) => (j === i ? { ...x, model: e.target.value } : x)))} />
              <select aria-label={`Tier of routed model ${i + 1}`} value={r.tier} onChange={(e) => setRoutes(routes.map((x, j) => (j === i ? { ...x, tier: e.target.value as RouteModel['tier'] } : x)))}>
                <option value="cheap">cheap</option>
                <option value="premium">premium</option>
              </select>
              <button type="button" className="link-button" onClick={() => setRoutes(routes.filter((_, j) => j !== i))} aria-label={`Remove routed model ${i + 1}`}>
                Remove
              </button>
            </div>
          ))}
          {routes.length < 6 ? (
            <button type="button" onClick={() => setRoutes([...routes, { model: '', tier: 'premium' }])}>
              Add a model
            </button>
          ) : null}
          <datalist id="route-model-suggestions">
            {suggestions.map((m) => (
              <option key={m} value={m} />
            ))}
          </datalist>
        </>
      ) : null}
    </fieldset>
  );
}
