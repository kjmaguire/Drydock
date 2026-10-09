import { useState } from 'react';
import type { AgentTemplate } from '../api/libraryClient';
import type { McpLibraryServer, McpServer } from '../api/mcpClient';
import type { Agent, Project } from '../api/types';
import { DropZone } from '../components/DropZone';
import { CliGateway } from '../components/CliGateway';
import { McpServerForm, inputOf } from '../components/McpServerForm';
import { TemplateForm } from '../components/TemplateForm';
import { Badge, ErrorNote, Page } from '../components/common';
import { startDrag, type Dragged } from '../lib/dnd';
import { useAction } from '../lib/useAction';
import { useClient } from '../state/context';
import { useAdapters, useLive } from '../state/hooks';
import { useReadOnly } from '../state/license';

// The Library: servers and team members defined once, then dropped into projects and onto agents. Everything that can be dragged
// also has a "Put in project" or "Give" control, so the page works from the keyboard.

type Say = (message: string) => void;
type Fail = (e: Error | undefined) => void;

/** Put an item into a project from a select, for people who do not drag. */
function PutInProject({ projects, label, onPick, busy }: { projects: readonly Project[]; label: string; onPick: (projectId: string) => void; busy: boolean }) {
  const [id, setId] = useState('');
  if (projects.length === 0) return <p className="muted">Create a project first.</p>;
  return (
    <div className="form-actions">
      <label>
        {label}
        <select aria-label={label} value={id || projects[0]!.id} onChange={(e) => setId(e.target.value)}>
          {projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      <button disabled={busy} onClick={() => onPick(id || projects[0]!.id)}>
        Put in project
      </button>
    </div>
  );
}

function McpCard({
  s,
  projects,
  onDragging,
  say,
  fail,
  onChanged,
}: {
  s: McpLibraryServer;
  projects: readonly Project[];
  onDragging: (d: Dragged | undefined) => void;
  say: Say;
  fail: Fail;
  onChanged: () => void;
}) {
  const client = useClient();
  const ro = useReadOnly();
  const act = useAction();
  const [editing, setEditing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [dragging, setDragging] = useState(false);
  const where = s.transport === 'http' ? s.url : (s.argv ?? []).join(' ');
  const inProjects = s.projects.map((id) => projects.find((p) => p.id === id)?.name ?? id);
  const setReadOnly = async (tool: string, on: boolean) => {
    const set = new Set(s.readOnlyTools);
    if (on) set.add(tool);
    else set.delete(tool);
    await act.run('readonly', () => client.mcpLibSave(inputOf(s, { readOnlyTools: [...set] })));
    onChanged();
  };
  return (
    <article
      className={`card dragitem${dragging ? ' is-dragging' : ''}`}
      draggable
      onDragStart={(e) => {
        setDragging(true);
        startDrag(e, { kind: 'mcp', id: s.id });
        onDragging({ kind: 'mcp', id: s.id });
      }}
      onDragEnd={() => {
        setDragging(false);
        onDragging(undefined);
      }}
      data-testid="lib-mcp"
      data-server={s.id}
    >
      <header className="card-head">
        <h3>{s.display}</h3>
        <code>{s.id}</code>
        <Badge>{s.transport === 'http' ? 'web address' : 'program'}</Badge>
        {s.guard ? <Badge title="Agents cannot send code, files, secrets or long text to it">send guard on</Badge> : <Badge tone="warn">send guard off</Badge>}
      </header>
      <p className="muted">Drag onto a project to add it there, or onto an agent to let that agent use it.</p>
      <ErrorNote error={act.error} what="Not changed" />
      <p>
        <code>{where}</code>
      </p>
      {s.secretEnv.length > 0 || s.hasToken ? (
        <p className="muted" data-testid="mcp-secrets">
          Secrets kept in the credential store: {s.hasToken ? 'bearer token' : s.secretEnv.join(', ')} (values are never shown)
        </p>
      ) : null}
      <p className="muted" data-testid="lib-mcp-projects">
        {inProjects.length === 0 ? 'Not in any project yet.' : `In: ${inProjects.join(', ')}.`}
      </p>
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
      <PutInProject
        projects={projects}
        label={`Put ${s.display} in project`}
        busy={act.busy !== ''}
        onPick={async (pid) => {
          const r = await act.run('add', () => client.mcpServerAdd(pid, s.id));
          if (r) {
            say(`${s.display} is now in ${projects.find((p) => p.id === pid)?.name ?? pid}.`);
            onChanged();
          }
        }}
      />
      <div className="form-actions">
        <button
          disabled={act.busy !== ''}
          onClick={async () => {
            const r = await act.run('discover', () => client.mcpLibDiscover(s.id));
            if (r) onChanged();
          }}
        >
          {act.busy === 'discover' ? 'Connecting...' : s.tools.length > 0 ? 'Test and refresh tools' : 'Find tools'}
        </button>
        <button onClick={() => setEditing((v) => !v)} aria-expanded={editing}>
          Edit
        </button>
        {confirm ? (
          <>
            <span>
              Disconnect {s.display}? It leaves {s.projects.length} project(s) and its stored secrets are deleted.
            </span>
            <button
              className="danger"
              disabled={act.busy !== ''}
              onClick={async () => {
                const done = await act.run('delete', async () => {
                  await client.mcpLibDelete(s.id);
                  return true;
                });
                if (done) {
                  fail(undefined);
                  say(`${s.display} was disconnected.`);
                  onChanged();
                }
              }}
            >
              Yes, disconnect
            </button>
            <button onClick={() => setConfirm(false)}>Keep</button>
          </>
        ) : (
          <button onClick={() => setConfirm(true)}>Disconnect</button>
        )}
      </div>
      {editing ? (
        <McpServerForm
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

function TemplateCard({
  t,
  servers,
  projects,
  adapters,
  onDragging,
  say,
  onChanged,
}: {
  t: AgentTemplate;
  servers: readonly McpLibraryServer[];
  projects: readonly Project[];
  adapters: Parameters<typeof TemplateForm>[0]['adapters'];
  onDragging: (d: Dragged | undefined) => void;
  say: Say;
  onChanged: () => void;
}) {
  const client = useClient();
  const act = useAction();
  const [editing, setEditing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [dragging, setDragging] = useState(false);
  const nameOf = (id: string) => servers.find((s) => s.id === id)?.display ?? id;
  return (
    <article
      className={`card dragitem${dragging ? ' is-dragging' : ''}`}
      draggable
      onDragStart={(e) => {
        setDragging(true);
        startDrag(e, { kind: 'template', id: t.id });
        onDragging({ kind: 'template', id: t.id });
      }}
      onDragEnd={() => {
        setDragging(false);
        onDragging(undefined);
      }}
      data-testid="lib-template"
      data-template={t.name}
    >
      <header className="card-head">
        <h3>{t.name}</h3>
        <span className="muted">{t.role}</span> <code>{t.adapter}</code>
        {t.model ? <span className="muted"> model {t.model}</span> : null}
        {t.effort ? <span className="muted"> effort {t.effort}</span> : null}
      </header>
      <p className="muted">Drag onto a project to add this team member there.</p>
      <ErrorNote error={act.error} what="Not changed" />
      {t.purpose ? <p>{t.purpose.length > 200 ? `${t.purpose.slice(0, 200)}...` : t.purpose}</p> : <p className="muted">No purpose written.</p>}
      {t.mcpServers.length > 0 ? (
        <ul className="chips" aria-label="MCP servers it gets">
          {t.mcpServers.map((id) => (
            <li key={id} className="chip">
              {nameOf(id)}
            </li>
          ))}
        </ul>
      ) : null}
      <PutInProject
        projects={projects}
        label={`Put ${t.name} in project`}
        busy={act.busy !== ''}
        onPick={async (pid) => {
          const r = await act.run('apply', () => client.agentTemplateApply(t.id, pid));
          if (r) {
            const pname = projects.find((p) => p.id === pid)?.name ?? pid;
            say(`${r.agent.name} joined ${pname}${r.granted.length ? `, with ${r.granted.map(nameOf).join(', ')}` : ''}${r.skipped.length ? `. Not granted: ${r.skipped.join('; ')}` : ''}.`);
            onChanged();
          }
        }}
      />
      <div className="form-actions">
        <button onClick={() => setEditing((v) => !v)} aria-expanded={editing}>
          Edit
        </button>
        {confirm ? (
          <>
            <span>Delete the template {t.name}? Agents already made from it stay.</span>
            <button
              className="danger"
              disabled={act.busy !== ''}
              onClick={async () => {
                const done = await act.run('delete', async () => {
                  await client.agentTemplateDelete(t.id);
                  return true;
                });
                if (done) onChanged();
              }}
            >
              Yes, delete
            </button>
            <button onClick={() => setConfirm(false)}>Keep</button>
          </>
        ) : (
          <button onClick={() => setConfirm(true)}>Delete</button>
        )}
      </div>
      {editing ? (
        <TemplateForm
          initial={t}
          adapters={adapters}
          servers={servers}
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

function ProjectPanel({
  project,
  servers,
  dragging,
  say,
  fail,
}: {
  project: Project;
  servers: readonly McpLibraryServer[];
  dragging: Dragged | undefined;
  say: Say;
  fail: Fail;
}) {
  const client = useClient();
  const ro = useReadOnly();
  const mine = useLive(() => client.mcpServerList(project.id), [project.id], (e) => e.type.startsWith('mcp.') || e.type.startsWith('agenttemplate.'));
  const team = useLive(() => client.agentList(project.id), [project.id], (e) => e.type.startsWith('agent.') || e.type.startsWith('agenttemplate.'));
  const act = useAction();
  const serverName = (id: string) => servers.find((s) => s.id === id)?.display ?? id;
  const inProject: McpServer[] = mine.data ?? [];
  const agents: Agent[] = team.data ?? [];
  const reload = () => {
    mine.reload();
    team.reload();
  };

  const dropOnProject = async (item: Dragged) => {
    fail(undefined);
    if (item.kind === 'mcp') {
      const r = await act.run('add', () => client.mcpServerAdd(project.id, item.id));
      if (r) say(`${serverName(item.id)} is now in ${project.name}.`);
    } else {
      const r = await act.run('apply', () => client.agentTemplateApply(item.id, project.id));
      if (r) say(`${r.agent.name} joined ${project.name}${r.granted.length ? `, with ${r.granted.map(serverName).join(', ')}` : ''}${r.skipped.length ? `. Not granted: ${r.skipped.join('; ')}` : ''}.`);
    }
    reload();
  };
  const grant = async (agent: Agent, serverId: string, on: boolean) => {
    fail(undefined);
    const r = await act.run('grant', () => client.mcpServerGrant(project.id, serverId, agent.id, on));
    if (r) say(on ? `${agent.name} can now use ${serverName(serverId)}.` : `${agent.name} can no longer use ${serverName(serverId)}.`);
    reload();
  };

  return (
    <DropZone
      accepts={['mcp', 'template']}
      dragging={dragging}
      onDropItem={dropOnProject}
      label={`Project ${project.name}: drop an MCP server or an agent template here`}
      testId="lib-project"
      className=""
    >
      <section className="card" data-project={project.name} aria-label={`Project ${project.name}`}>
        <header className="card-head">
          <h3>{project.name}</h3>
          {dragging ? <Badge tone="info">drop here</Badge> : null}
        </header>
        <ErrorNote error={act.error ?? mine.error ?? team.error} what="Not done" />
        <h4>MCP servers in this project</h4>
        {inProject.length === 0 ? (
          <p className="muted">None. Drop a server here.</p>
        ) : (
          <ul className="chips" aria-label={`MCP servers in ${project.name}`}>
            {inProject.map((s) => (
              <li key={s.id} className="chip" data-testid="proj-server" data-server={s.id}>
                {s.display}
                <button
                  aria-label={`Take ${s.display} out of ${project.name}`}
                  disabled={act.busy !== '' || ro.readOnly}
                  onClick={async () => {
                    await act.run('remove', () => client.mcpServerRemove(project.id, s.id));
                    say(`${s.display} was taken out of ${project.name}.`);
                    reload();
                  }}
                >
                  ×
                </button>
              </li>
            ))}
          </ul>
        )}
        <h4>Team</h4>
        {agents.length === 0 ? <p className="muted">No agents. Drop a template here to add one.</p> : null}
        {agents.map((a) => {
          const mineIds = inProject.filter((s) => s.agents.some((g) => g.agentId === a.id));
          const options = servers.filter((s) => !mineIds.some((m) => m.id === s.id));
          return (
            <DropZone
              key={a.id}
              accepts={['mcp']}
              dragging={dragging}
              onDropItem={(item) => grant(a, item.id, true)}
              label={`Agent ${a.name}: drop an MCP server here to let it use the server`}
              testId="lib-agent"
            >
              <div data-agent={a.name}>
                <strong>{a.name}</strong> <span className="muted">{a.role}</span> <code>{a.adapter}</code>
                {mineIds.length > 0 ? (
                  <ul className="chips" aria-label={`Servers ${a.name} may use`}>
                    {mineIds.map((s) => (
                      <li key={s.id} className="chip" data-testid="agent-server" data-server={s.id}>
                        {s.display}
                        <button aria-label={`Stop ${a.name} using ${s.display}`} disabled={act.busy !== '' || ro.readOnly} onClick={() => grant(a, s.id, false)}>
                          ×
                        </button>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <span className="muted"> no MCP servers</span>
                )}
                {options.length > 0 ? <GiveServer agent={a} options={options} busy={act.busy !== '' || ro.readOnly} onGive={(sid) => grant(a, sid, true)} /> : null}
              </div>
            </DropZone>
          );
        })}
      </section>
    </DropZone>
  );
}

function GiveServer({ agent, options, busy, onGive }: { agent: Agent; options: readonly McpLibraryServer[]; busy: boolean; onGive: (serverId: string) => void }) {
  const [id, setId] = useState('');
  return (
    <span className="form-actions">
      <select aria-label={`Server to give ${agent.name}`} value={id || options[0]!.id} onChange={(e) => setId(e.target.value)}>
        {options.map((s) => (
          <option key={s.id} value={s.id}>
            {s.display}
          </option>
        ))}
      </select>
      <button disabled={busy} onClick={() => onGive(id || options[0]!.id)}>
        Give to {agent.name}
      </button>
    </span>
  );
}

export default function LibraryPage({ projects }: { projects: readonly Project[] }) {
  const client = useClient();
  const adapters = useAdapters();
  const servers = useLive(() => client.mcpLibList(), [], (e) => e.type.startsWith('mcp.server'));
  const templates = useLive(() => client.agentTemplateList(), [], (e) => e.type.startsWith('agenttemplate.'));
  const [dragging, setDragging] = useState<Dragged | undefined>();
  const [note, setNote] = useState('');
  const [error, setError] = useState<Error | undefined>();
  const [connecting, setConnecting] = useState(false);
  const [newTemplate, setNewTemplate] = useState(false);
  const libServers = servers.data ?? [];
  const tpls = templates.data ?? [];
  const reload = () => {
    servers.reload();
    templates.reload();
  };
  return (
    <Page title="Library">
      <p className="muted">
        Connect an MCP server or define a team member once, here. Then drag it onto a project to add it there, or drag a server onto an agent to let that agent use it. A project only ever
        reaches the servers it was given, and an agent only the servers you gave it.
      </p>
      <div role="status" aria-live="polite" data-testid="library-note">
        {note}
      </div>
      <ErrorNote error={error ?? servers.error ?? templates.error} what="Library" />
      <div className="library-cols">
        <div>
          <h2>MCP servers</h2>
          {servers.loading && !servers.data ? <p className="muted">Loading...</p> : null}
          {!servers.loading && libServers.length === 0 ? <p className="muted">No servers are connected yet.</p> : null}
          {libServers.map((s) => (
            <McpCard key={s.id} s={s} projects={projects} onDragging={setDragging} say={setNote} fail={setError} onChanged={reload} />
          ))}
          {connecting ? (
            <McpServerForm
              onCancel={() => setConnecting(false)}
              onDone={(saved) => {
                setConnecting(false);
                setNote(`${saved.display} is connected. Press Find tools, then drag it onto a project.`);
                reload();
              }}
            />
          ) : (
            <div className="form-actions">
              <button className="primary" onClick={() => setConnecting(true)}>
                Connect an MCP server
              </button>
            </div>
          )}

          <CliGateway />

          <h2>Agent templates</h2>
          {!templates.loading && tpls.length === 0 ? <p className="muted">No templates yet. Make one here, or save an agent as a template on the Team page.</p> : null}
          {tpls.map((t) => (
            <TemplateCard key={t.id} t={t} servers={libServers} projects={projects} adapters={adapters.data ?? []} onDragging={setDragging} say={setNote} onChanged={reload} />
          ))}
          {newTemplate ? (
            <TemplateForm
              adapters={adapters.data ?? []}
              servers={libServers}
              onCancel={() => setNewTemplate(false)}
              onDone={(saved) => {
                setNewTemplate(false);
                setNote(`The template ${saved.name} was saved.`);
                reload();
              }}
            />
          ) : (
            <div className="form-actions">
              <button className="primary" onClick={() => setNewTemplate(true)}>
                New agent template
              </button>
            </div>
          )}
        </div>
        <div>
          <h2>Projects</h2>
          {projects.length === 0 ? <p className="muted">No projects yet.</p> : null}
          {projects.map((p) => (
            <ProjectPanel key={p.id} project={p} servers={libServers} dragging={dragging} say={setNote} fail={setError} />
          ))}
        </div>
      </div>
    </Page>
  );
}
