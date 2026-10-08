import { useState, type FormEvent } from 'react';
import type { Agent } from '../api/types';
import type { McpApproval, McpServer, McpServerInput } from '../api/mcpClient';
import { formatDateTime } from '../lib/format';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { ReadOnlyNote, useReadOnly } from '../state/license';
import { Badge, ErrorNote, Page } from '../components/common';

/** One argument per line, so Windows paths and quoted values need no shell quoting rules. */
export const parseLines = (s: string): string[] => s.split('\n').map((l) => l.replace(/\r$/, '')).filter((l) => l.trim() !== '');

/** NAME=value per line. Throws a message the form shows. */
export function parseEnv(s: string, what: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of parseLines(s)) {
    const i = line.indexOf('=');
    if (i <= 0) throw new Error(`${what}: "${line.trim()}" is not NAME=value`);
    out[line.slice(0, i).trim()] = line.slice(i + 1);
  }
  return out;
}

/** The save input that keeps everything about a server as it is (secrets stay in the store, empty values keep them). */
export function inputOf(s: McpServer, over: Partial<McpServerInput> = {}): McpServerInput {
  return {
    projectId: s.projectId, id: s.id, display: s.display, transport: s.transport, url: s.url, argv: s.argv, cwd: s.cwd, env: s.env,
    secretEnv: Object.fromEntries(s.secretEnv.map((n) => [n, ''])), guard: s.guard, readOnlyTools: s.readOnlyTools, enabled: s.enabled, ...over,
  };
}

function Approvals({ projectId, agents }: { projectId: string; agents: readonly Agent[] }) {
  const client = useClient();
  const ro = useReadOnly();
  const act = useAction();
  const q = useLive(() => client.mcpApprovalList(projectId), [projectId], (e) => e.type.startsWith('approval.') || e.type.startsWith('mcp.'));
  const rows: McpApproval[] = q.data ?? [];
  if (rows.length === 0) return null;
  const name = (id: string) => agents.find((a) => a.id === id)?.name ?? id;
  return (
    <section aria-label="Calls waiting for you" data-testid="mcp-approvals">
      <h2>Waiting for your approval</h2>
      <p className="muted">An agent called a tool that is not marked read-only. The call has not been made. Approve only the exact call shown.</p>
      <ErrorNote error={act.error} what="Not sent" />
      {rows.map((a) => (
        <article key={a.id} className="card" data-testid="mcp-approval" data-approval={a.id}>
          <header className="card-head">
            <h3>
              <code>{a.server}</code> / <code>{a.tool}</code>
            </h3>
            <span className="muted">
              {name(a.agentId)}, asked {formatDateTime(a.created)}
            </span>
          </header>
          <p className="muted">Arguments the agent sent (text from an agent, shown as is):</p>
          <pre className="md-code" tabIndex={0}>{a.args}</pre>
          <div className="form-actions">
            <button
              className="primary"
              disabled={act.busy !== '' || ro.readOnly}
              title={ro.readOnly ? ro.reason : undefined}
              onClick={async () => {
                await act.run('approve', () => client.mcpApprovalDecide(a.id, true));
                q.reload();
              }}
            >
              Approve this call
            </button>
            <button
              disabled={act.busy !== ''}
              onClick={async () => {
                await act.run('reject', () => client.mcpApprovalDecide(a.id, false));
                q.reload();
              }}
            >
              Reject
            </button>
          </div>
        </article>
      ))}
    </section>
  );
}

interface SecretRow {
  name: string;
  value: string;
  existing: boolean;
}

function ServerForm({ projectId, initial, onDone, onCancel }: { projectId: string; initial?: McpServer; onDone: () => void; onCancel: () => void }) {
  const client = useClient();
  const ro = useReadOnly();
  const act = useAction();
  const [localErr, setLocalErr] = useState<Error | undefined>();
  const [id, setId] = useState(initial?.id ?? '');
  const [display, setDisplay] = useState(initial?.display ?? '');
  const [transport, setTransport] = useState<'stdio' | 'http'>(initial?.transport ?? 'stdio');
  const [url, setUrl] = useState(initial?.url ?? '');
  const [argv, setArgv] = useState((initial?.argv ?? []).join('\n'));
  const [cwd, setCwd] = useState(initial?.cwd ?? '');
  const [env, setEnv] = useState(Object.entries(initial?.env ?? {}).map(([k, v]) => `${k}=${v}`).join('\n'));
  const [secrets, setSecrets] = useState<SecretRow[]>((initial?.secretEnv ?? []).map((n) => ({ name: n, value: '', existing: true })));
  const [token, setToken] = useState('');
  const [clearToken, setClearToken] = useState(false);
  const [guard, setGuard] = useState(initial?.guard ?? true);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setLocalErr(undefined);
    let envObj: Record<string, string>;
    try {
      envObj = parseEnv(env, 'Environment');
    } catch (err) {
      setLocalErr(err as Error);
      return;
    }
    const input: McpServerInput = {
      projectId, id: id.trim(), display: display.trim(), transport, guard,
      readOnlyTools: initial?.readOnlyTools ?? [], enabled: initial?.enabled ?? true,
      ...(transport === 'http'
        ? { url: url.trim(), bearerToken: token.trim() || undefined, clearBearer: clearToken || undefined }
        : { argv: parseLines(argv), cwd: cwd.trim() || undefined, env: envObj, secretEnv: Object.fromEntries(secrets.filter((s) => s.name.trim()).map((s) => [s.name.trim(), s.value])) }),
    };
    const r = await act.run('save', () => client.mcpServerSave(input));
    setToken('');
    if (r) onDone();
  };

  return (
    <form className="form form-grid" onSubmit={submit} aria-label={initial ? `Edit ${initial.display}` : 'Add an MCP server'} data-testid="mcp-form">
      <ErrorNote error={localErr ?? act.error} what="Server not saved" />
      <ReadOnlyNote />
      <label>
        Short id (letters and digits, at most 8; agents see its tools as <code>id__tool</code>)
        <input value={id} onChange={(e) => setId(e.target.value)} disabled={!!initial} required pattern="[a-z][a-z0-9]{0,7}" spellCheck={false} />
      </label>
      <label>
        Name
        <input value={display} onChange={(e) => setDisplay(e.target.value)} required />
      </label>
      <label>
        Kind
        <select aria-label="Kind" value={transport} onChange={(e) => setTransport(e.target.value as 'stdio' | 'http')}>
          <option value="stdio">A program on this computer (stdio)</option>
          <option value="http">A web address (https)</option>
        </select>
      </label>
      {transport === 'http' ? (
        <>
          <label>
            Address (https, or http on this computer only)
            <input value={url} onChange={(e) => setUrl(e.target.value)} required spellCheck={false} placeholder="https://example.com/mcp" />
          </label>
          <label>
            Bearer token {initial?.hasToken ? <Badge tone="ok">set</Badge> : null}
            <input type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} placeholder={initial?.hasToken ? 'leave empty to keep the stored token' : 'optional'} />
          </label>
          {initial?.hasToken ? (
            <label className="inline">
              <input type="checkbox" checked={clearToken} onChange={(e) => setClearToken(e.target.checked)} /> Remove the stored token
            </label>
          ) : null}
        </>
      ) : (
        <>
          <label>
            Command, one argument per line (the program first)
            <textarea value={argv} onChange={(e) => setArgv(e.target.value)} rows={3} required spellCheck={false} placeholder={'npx\n-y\n@modelcontextprotocol/server-everything@1.0.0'} />
          </label>
          <label>
            Working folder (optional)
            <input value={cwd} onChange={(e) => setCwd(e.target.value)} spellCheck={false} />
          </label>
          <label>
            Environment, NAME=value per line (not secret)
            <textarea value={env} onChange={(e) => setEnv(e.target.value)} rows={2} spellCheck={false} />
          </label>
          <fieldset>
            <legend>Secret environment variables (kept in the Windows credential store, never shown again)</legend>
            {secrets.map((s, i) => (
              <div key={i} className="form-actions">
                <input aria-label={`Secret name ${i + 1}`} value={s.name} disabled={s.existing} onChange={(e) => setSecrets(secrets.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} placeholder="API_KEY" spellCheck={false} />
                <input
                  aria-label={`Secret value ${i + 1}`}
                  type="password"
                  autoComplete="off"
                  value={s.value}
                  onChange={(e) => setSecrets(secrets.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))}
                  placeholder={s.existing ? 'leave empty to keep' : 'value'}
                />
                <button type="button" onClick={() => setSecrets(secrets.filter((_, j) => j !== i))}>
                  Remove
                </button>
              </div>
            ))}
            <button type="button" onClick={() => setSecrets([...secrets, { name: '', value: '', existing: false }])}>
              Add a secret
            </button>
          </fieldset>
        </>
      )}
      <label className="inline">
        <input type="checkbox" checked={guard} onChange={(e) => setGuard(e.target.checked)} /> Stop agents from sending code, files, secrets or long text to this server
      </label>
      <div className="form-actions">
        <button type="submit" className="primary" disabled={act.busy !== '' || ro.readOnly} title={ro.readOnly ? ro.reason : undefined}>
          {act.busy === 'save' ? 'Saving...' : initial ? 'Save changes' : 'Add server'}
        </button>
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function ServerCard({ s, agents, onChanged }: { s: McpServer; agents: readonly Agent[]; onChanged: () => void }) {
  const client = useClient();
  const ro = useReadOnly();
  const act = useAction();
  const [editing, setEditing] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const granted = (agentId: string) => s.agents.some((a) => a.agentId === agentId);
  const setAccess = async (agentId: string, on: boolean) => {
    const next = on ? [...s.agents, { agentId, tools: ['*'] }] : s.agents.filter((a) => a.agentId !== agentId);
    await act.run('access', () => client.mcpServerAccessSet(s.projectId, s.id, next));
    onChanged();
  };
  const setReadOnly = async (tool: string, on: boolean) => {
    const set = new Set(s.readOnlyTools);
    if (on) set.add(tool);
    else set.delete(tool);
    await act.run('readonly', () => client.mcpServerSave(inputOf(s, { readOnlyTools: [...set] })));
    onChanged();
  };
  const where = s.transport === 'http' ? s.url : (s.argv ?? []).join(' ');
  return (
    <article className="card" data-testid="mcp-server" data-server={s.id}>
      <header className="card-head">
        <h3>{s.display}</h3>
        <code>{s.id}</code>
        <Badge tone={s.enabled ? 'ok' : 'muted'}>{s.enabled ? 'on' : 'off'}</Badge>
        <Badge title="Where it runs">{s.transport === 'http' ? 'web address' : 'program'}</Badge>
        {s.guard ? <Badge title="Agents cannot send code, files, secrets or long text to it">send guard on</Badge> : <Badge tone="warn">send guard off</Badge>}
      </header>
      <ErrorNote error={act.error} what="Not changed" />
      <p>
        <code>{where}</code>
      </p>
      {s.secretEnv.length > 0 || s.hasToken ? (
        <p className="muted" data-testid="mcp-secrets">
          Secrets kept in the credential store: {s.hasToken ? 'bearer token' : s.secretEnv.join(', ')} (values are never shown)
        </p>
      ) : null}

      <h4>Tools</h4>
      {s.tools.length === 0 ? (
        <p className="muted">Tools are not known yet. Press Find tools to start the server and list them.</p>
      ) : (
        <table className="table" aria-label={`Tools of ${s.display}`}>
          <thead>
            <tr>
              <th scope="col">Tool</th>
              <th scope="col">Reads only (no approval)</th>
            </tr>
          </thead>
          <tbody>
            {s.tools.map((t) => (
              <tr key={t.name} data-testid="mcp-tool" data-tool={t.name}>
                <td>
                  <code>{t.name}</code>
                  {t.description ? <div className="muted">{t.description}</div> : null}
                </td>
                <td>
                  <label className="inline">
                    <input type="checkbox" aria-label={`${t.name} only reads`} checked={s.readOnlyTools.includes(t.name)} disabled={act.busy !== '' || ro.readOnly} onChange={(e) => setReadOnly(t.name, e.target.checked)} />
                    {t.readOnlyHint ? <span className="muted"> the server says it only reads</span> : null}
                  </label>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <p className="muted">Every tool you do not tick needs your approval for each call. Ticking is your statement that the tool cannot change anything.</p>

      <h4>Which agents may use it</h4>
      {agents.length === 0 ? <p className="muted">No agents on this project yet.</p> : null}
      <ul className="agent-list" aria-label={`Agents allowed to use ${s.display}`}>
        {agents.map((a) => (
          <li key={a.id}>
            <label className="inline">
              <input type="checkbox" aria-label={`${a.name} may use ${s.display}`} checked={granted(a.id)} disabled={act.busy !== ''} onChange={(e) => setAccess(a.id, e.target.checked)} /> {a.name} <span className="muted">{a.role}</span>
            </label>
          </li>
        ))}
      </ul>
      <p className="muted">No agent can use a server until you tick it here.</p>

      <div className="form-actions">
        <button
          disabled={act.busy !== ''}
          onClick={async () => {
            await act.run('discover', () => client.mcpServerDiscover(s.projectId, s.id));
            onChanged();
          }}
        >
          {act.busy === 'discover' ? 'Connecting...' : s.tools.length > 0 ? 'Test and refresh tools' : 'Find tools'}
        </button>
        <button onClick={() => setEditing((v) => !v)} aria-expanded={editing}>
          Edit
        </button>
        <button
          disabled={act.busy !== '' || ro.readOnly}
          title={ro.readOnly ? ro.reason : undefined}
          onClick={async () => {
            await act.run('toggle', () => client.mcpServerSave(inputOf(s, { enabled: !s.enabled })));
            onChanged();
          }}
        >
          {s.enabled ? 'Turn off' : 'Turn on'}
        </button>
        {confirmRemove ? (
          <>
            <span>Remove {s.display} and its stored secrets?</span>
            <button
              className="danger"
              disabled={act.busy !== ''}
              onClick={async () => {
                const done = await act.run('delete', async () => {
                  await client.mcpServerDelete(s.projectId, s.id);
                  return true;
                });
                if (done) onChanged();
              }}
            >
              Yes, remove
            </button>
            <button onClick={() => setConfirmRemove(false)}>Keep</button>
          </>
        ) : (
          <button onClick={() => setConfirmRemove(true)}>Remove</button>
        )}
      </div>
      {editing ? (
        <ServerForm
          projectId={s.projectId}
          initial={s}
          onCancel={() => setEditing(false)}
          onDone={() => {
            setEditing(false);
            onChanged();
          }}
        />
      ) : null}
    </article>
  );
}

export default function McpServersView({ projectId, agents }: { projectId: string; agents: readonly Agent[] }) {
  const client = useClient();
  const q = useLive(() => client.mcpServerList(projectId), [projectId], (e) => e.type.startsWith('mcp.server') || e.type === 'mcp.access_changed' || e.type.startsWith('agent.'));
  const [adding, setAdding] = useState(false);
  const servers = q.data ?? [];
  return (
    <Page title="MCP servers">
      <p className="muted">
        An MCP server gives agents extra tools: documentation search, an issue tracker, a database. You add the servers for this project, and you choose which agents may use each one. A server nobody
        was granted is never reached. Anything a server returns is treated as outside text, and every tool you have not marked read-only waits for your approval, call by call. Secrets stay in the
        Windows credential store. Only Claude Code agents receive these servers so far; other agent programs are not verified.
      </p>
      <ErrorNote error={q.error} what="Could not load the servers" />
      <Approvals projectId={projectId} agents={agents} />
      <h2>Servers</h2>
      {q.loading && !q.data ? <p className="muted">Loading...</p> : null}
      {!q.loading && servers.length === 0 ? <p className="muted">No MCP servers on this project yet.</p> : null}
      {servers.map((s) => (
        <ServerCard key={s.id} s={s} agents={agents} onChanged={q.reload} />
      ))}
      {adding ? (
        <ServerForm
          projectId={projectId}
          onCancel={() => setAdding(false)}
          onDone={() => {
            setAdding(false);
            q.reload();
          }}
        />
      ) : (
        <div className="form-actions">
          <button className="primary" onClick={() => setAdding(true)}>
            Add an MCP server
          </button>
        </div>
      )}
    </Page>
  );
}
