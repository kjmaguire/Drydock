import { useState } from 'react';
import type { Agent } from '../api/types';
import type { McpApproval, McpServer } from '../api/mcpClient';
import { formatDateTime } from '../lib/format';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useLive } from '../state/hooks';
import { useReadOnly } from '../state/license';
import { Badge, ErrorNote, Page } from '../components/common';

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

function ServerCard({ s, agents, onChanged }: { s: McpServer; agents: readonly Agent[]; onChanged: () => void }) {
  const client = useClient();
  const ro = useReadOnly();
  const act = useAction();
  const [confirmRemove, setConfirmRemove] = useState(false);
  const granted = (agentId: string) => s.agents.some((a) => a.agentId === agentId);
  const where = s.transport === 'http' ? s.url : (s.argv ?? []).join(' ');
  return (
    <article className="card" data-testid="mcp-server" data-server={s.id}>
      <header className="card-head">
        <h3>{s.display}</h3>
        <code>{s.id}</code>
        <Badge tone={s.enabled ? 'ok' : 'muted'}>{s.enabled ? 'on' : 'off in this project'}</Badge>
        <Badge title="Where it runs">{s.transport === 'http' ? 'web address' : 'program'}</Badge>
        {s.guard ? <Badge title="Agents cannot send code, files, secrets or long text to it">send guard on</Badge> : <Badge tone="warn">send guard off</Badge>}
        {s.projects.length > 1 ? <Badge title="The same connected server is in several projects; each project has its own agents and grants">in {s.projects.length} projects</Badge> : null}
      </header>
      <ErrorNote error={act.error} what="Not changed" />
      <p>
        <code>{where}</code>
      </p>

      <h4>Tools</h4>
      {s.tools.length === 0 ? (
        <p className="muted">
          Tools are not known yet. Open the <a href="#/library">Library</a> and press Find tools.
        </p>
      ) : (
        <ul aria-label={`Tools of ${s.display}`}>
          {s.tools.map((t) => (
            <li key={t.name} data-testid="mcp-tool" data-tool={t.name}>
              <code>{t.name}</code> {s.readOnlyTools.includes(t.name) ? <Badge tone="ok">reads only</Badge> : <Badge tone="warn">asks you first</Badge>}
            </li>
          ))}
        </ul>
      )}
      <p className="muted">Which tools only read is decided where the server is connected, in the Library. Every other tool waits for your approval, call by call.</p>

      <h4>Which agents of this project may use it</h4>
      {agents.length === 0 ? <p className="muted">No agents on this project yet.</p> : null}
      <ul className="agent-list" aria-label={`Agents allowed to use ${s.display}`}>
        {agents.map((a) => (
          <li key={a.id}>
            <label className="inline">
              <input
                type="checkbox"
                aria-label={`${a.name} may use ${s.display}`}
                checked={granted(a.id)}
                disabled={act.busy !== ''}
                onChange={async (e) => {
                  await act.run('access', () => client.mcpServerGrant(s.projectId, s.id, a.id, e.target.checked));
                  onChanged();
                }}
              />{' '}
              {a.name} <span className="muted">{a.role}</span>
            </label>
          </li>
        ))}
      </ul>
      <p className="muted">No agent can use a server until you tick it here.</p>

      <div className="form-actions">
        <button
          disabled={act.busy !== '' || ro.readOnly}
          title={ro.readOnly ? ro.reason : undefined}
          onClick={async () => {
            await act.run('toggle', () => client.mcpServerSetEnabled(s.projectId, s.id, !s.enabled));
            onChanged();
          }}
        >
          {s.enabled ? 'Turn off in this project' : 'Turn on in this project'}
        </button>
        {confirmRemove ? (
          <>
            <span>Take {s.display} out of this project? Its grants here go too; it stays in the Library.</span>
            <button
              className="danger"
              disabled={act.busy !== ''}
              onClick={async () => {
                const done = await act.run('remove', async () => {
                  await client.mcpServerRemove(s.projectId, s.id);
                  return true;
                });
                if (done) onChanged();
              }}
            >
              Yes, take it out
            </button>
            <button onClick={() => setConfirmRemove(false)}>Keep</button>
          </>
        ) : (
          <button onClick={() => setConfirmRemove(true)}>Take out of this project</button>
        )}
      </div>
    </article>
  );
}

function AddFromLibrary({ projectId, have, onAdded }: { projectId: string; have: Set<string>; onAdded: () => void }) {
  const client = useClient();
  const act = useAction();
  const lib = useLive(() => client.mcpLibList(), [], (e) => e.type.startsWith('mcp.server'));
  const [pick, setPick] = useState('');
  const options = (lib.data ?? []).filter((x) => !have.has(x.id));
  return (
    <section aria-label="Add a server from the library">
      <h2>Add from the library</h2>
      <ErrorNote error={lib.error ?? act.error} what="Not added" />
      {options.length === 0 ? (
        <p className="muted">
          {(lib.data ?? []).length === 0 ? 'No servers are connected yet.' : 'Every connected server is already in this project.'} Connect servers once in the <a href="#/library">Library</a>, then
          put them into projects here or by dragging them there.
        </p>
      ) : (
        <div className="form-actions">
          <label>
            Server
            <select aria-label="Server to add" value={pick || options[0]!.id} onChange={(e) => setPick(e.target.value)}>
              {options.map((x) => (
                <option key={x.id} value={x.id}>
                  {x.display} ({x.id})
                </option>
              ))}
            </select>
          </label>
          <button
            className="primary"
            disabled={act.busy !== ''}
            onClick={async () => {
              const r = await act.run('add', () => client.mcpServerAdd(projectId, pick || options[0]!.id));
              setPick('');
              if (r) onAdded();
            }}
          >
            Put into this project
          </button>
        </div>
      )}
    </section>
  );
}

export default function McpServersView({ projectId, agents }: { projectId: string; agents: readonly Agent[] }) {
  const client = useClient();
  const q = useLive(() => client.mcpServerList(projectId), [projectId], (e) => e.type.startsWith('mcp.server') || e.type === 'mcp.access_changed' || e.type.startsWith('agent.'));
  const servers = q.data ?? [];
  return (
    <Page title="MCP servers">
      <p className="muted">
        An MCP server gives agents extra tools: documentation search, an issue tracker, a database. You connect a server once in the <a href="#/library">Library</a>, put it into the projects
        that should have it, and choose which agents of each project may use it. A server nobody was granted is never reached. Anything a server returns is treated as outside text, and every
        tool you have not marked read-only waits for your approval, call by call. Secrets stay in the Windows credential store. Only Claude Code agents receive these servers so far; other
        agent programs are not verified.
      </p>
      <ErrorNote error={q.error} what="Could not load the servers" />
      <Approvals projectId={projectId} agents={agents} />
      <h2>In this project</h2>
      {q.loading && !q.data ? <p className="muted">Loading...</p> : null}
      {!q.loading && servers.length === 0 ? <p className="muted">No MCP servers in this project yet.</p> : null}
      {servers.map((s) => (
        <ServerCard key={s.id} s={s} agents={agents} onChanged={q.reload} />
      ))}
      <AddFromLibrary projectId={projectId} have={new Set(servers.map((s) => s.id))} onAdded={q.reload} />
    </Page>
  );
}
