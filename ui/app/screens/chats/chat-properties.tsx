// A chat's properties (decision P60): which agent CLI and where it runs, the working folder, the skills from the workspace
// skill library and the MCP servers from the workspace MCP library. The pickers are shared by the new chat dialog and the
// properties dialog of an open chat. What is chosen reaches the chat's CLI through Drydock's MCP gateway from the next
// message on; a new CLI, distro or folder starts a new session that is given the conversation so far.

import { css, on, type Handle } from 'remix/component';
import type { Chat, ChatMcpGrant, ChatSkillRef, ChatUpdate } from '../../api/chatClient.ts';
import type { LibrarySkill } from '../../api/extTypes.ts';
import type { McpApproval, McpLibraryServer } from '../../api/mcpClient.ts';
import type { AdapterInfo, Connection } from '../../api/types.ts';
import { routes } from '../../routes.ts';
import { action, query, services, type AnyHandle } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox, Field, formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { chatChoices, firstSentence, grantableTools, pickableSkills, sameServers, sameSkills, skillKey, skillUsable, toggleServer, toggleSkill } from './chats.ts';
import { ChooseFolder, folderRow } from '../shared/choose-folder.tsx';

const groupCss = css({
  display: 'grid',
  gap: t.s2,
  minWidth: '0',
  margin: '0',
  padding: `${t.s2} ${t.s3} ${t.s3}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  '& legend': { padding: '0 4px', fontWeight: '600', fontSize: t.textSm },
});
const hintCss = css({ margin: '0', color: t.textMuted, fontSize: t.textSm });
const listCss = css({
  display: 'grid',
  gap: '2px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  maxHeight: '220px',
  overflowY: 'auto',
  '& small': { overflowWrap: 'anywhere' },
});

/** Read the workspace skill library (every registry), while `enabled`. */
export function useSkillLibrary(handle: AnyHandle, enabled: () => boolean) {
  const s = services(handle);
  return query(handle, {
    enabled,
    when: (e) => e.type.startsWith('skill.'),
    load: () => s.client.skillLibraryList(),
  });
}

/** Read the workspace MCP library, while `enabled`. */
export function useMcpLibrary(handle: AnyHandle, enabled: () => boolean) {
  const s = services(handle);
  return query(handle, {
    enabled,
    when: (e) => e.type.startsWith('mcp.') || e.type.startsWith('mcplib.'),
    load: () => s.client.mcpLibList(),
  });
}

/** Skills from the workspace library: a filter and one checkbox per skill, the chosen ones first. */
export function SkillPicker(
  handle: Handle<{ library: LibrarySkill[] | undefined; loading?: boolean; error?: Error; chosen: ChatSkillRef[]; onChange: (next: ChatSkillRef[]) => void }>,
) {
  let filter = '';
  return () => {
    const { library, loading, error, chosen, onChange } = handle.props;
    const picked = new Set(chosen.map(skillKey));
    const shown = pickableSkills(library ?? [], chosen, filter);
    const missing = chosen.filter((c) => library && !library.some((k) => skillKey(k) === skillKey(c)));
    return (
      <fieldset mix={groupCss} data-testid="chat-skills-picker">
        <legend>Skills</legend>
        <p mix={hintCss}>From your skill library. The agent is told which skills it has and reads one when the work needs it.</p>
        <input type="search" aria-label="Filter skills" placeholder="Filter skills" value={filter} spellcheck={false} mix={on('input', (e) => ((filter = e.currentTarget.value), void handle.update()))} />
        <ErrorNote error={error} what="Could not read the skill library" />
        {loading && !library ? <Loading label="Reading the skill library..." /> : null}
        {library && library.length === 0 ? (
          <p mix={hintCss}>
            The library has no skills yet: <a href={routes.librarySkills.href()}>Library, Skills</a> syncs them from your registries.
          </p>
        ) : null}
        {shown.length || missing.length ? (
          <ul mix={listCss} aria-label="Library skills">
            {shown.map((k) => {
              const on_ = picked.has(skillKey(k));
              const why = k.gone ? 'no longer in its registry' : !skillUsable(k) ? `blocked: ${k.blockReason || 'it did not pass the checks'}` : '';
              return (
                <li key={skillKey(k)} data-testid="chat-skill-option" data-name={k.name} data-registry={k.registryId}>
                  <Checkbox
                    label={k.name}
                    description={why ? `${k.registryId} · ${why}` : `${k.registryId} · ${firstSentence(k.description) || 'no description'}`}
                    checked={on_}
                    disabled={!on_ && !!why}
                    onChange={(v) => onChange(toggleSkill(chosen, k, v))}
                  />
                </li>
              );
            })}
            {missing.map((c) => (
              <li key={skillKey(c)} data-testid="chat-skill-option" data-name={c.name} data-registry={c.registryId}>
                <Checkbox label={c.name} description={`${c.registryId} · not in the library any more`} checked onChange={(v) => onChange(toggleSkill(chosen, c, v))} />
              </li>
            ))}
          </ul>
        ) : library && library.length > 0 ? (
          <p mix={hintCss}>No skill matches.</p>
        ) : null}
        <small mix={hintCss} data-testid="chat-skills-count">
          {chosen.length === 0 ? 'None chosen' : `${chosen.length} chosen`}
        </small>
      </fieldset>
    );
  };
}

/** MCP servers from the workspace library: one checkbox per connected server. */
export function ServerPicker(
  handle: Handle<{ servers: McpLibraryServer[] | undefined; loading?: boolean; error?: Error; chosen: ChatMcpGrant[]; had?: ChatMcpGrant[]; onChange: (next: ChatMcpGrant[]) => void }>,
) {
  return () => {
    const { servers, loading, error, chosen, had = [], onChange } = handle.props;
    const missing = chosen.filter((g) => servers && !servers.some((sv) => sv.id === g.serverId));
    return (
      <fieldset mix={groupCss} data-testid="chat-mcp-picker">
        <legend>MCP servers</legend>
        <p mix={hintCss}>Connected once in the Library. The agent reaches a server through Drydock's gateway; a tool you did not mark read-only asks you first.</p>
        <ErrorNote error={error} what="Could not read the MCP servers" />
        {loading && !servers ? <Loading label="Reading the MCP servers..." /> : null}
        {servers && servers.length === 0 ? (
          <p mix={hintCss}>
            No MCP server is connected yet: <a href={routes.library.href()}>connect one in the Library</a>.
          </p>
        ) : null}
        {servers?.length || missing.length ? (
          <ul mix={listCss} aria-label="MCP servers">
            {(servers ?? []).map((sv) => {
              const g = chosen.find((x) => x.serverId === sv.id);
              const tools = g?.tools.length ? `Tools: ${g.tools.join(', ')}` : sv.discoveredAt ? `${grantableTools(sv).length} tools` : 'Its tools are listed when you choose it';
              return (
                <li key={sv.id} data-testid="chat-mcp-option" data-server={sv.id}>
                  <Checkbox label={sv.display || sv.id} description={sv.lastError ? `${tools} · last error: ${sv.lastError}` : tools} checked={!!g} onChange={(v) => onChange(toggleServer(chosen, sv.id, v, had))} />
                </li>
              );
            })}
            {missing.map((g) => (
              <li key={g.serverId} data-testid="chat-mcp-option" data-server={g.serverId}>
                <Checkbox label={g.serverId} description="not connected any more" checked onChange={(v) => onChange(toggleServer(chosen, g.serverId, v, had))} />
              </li>
            ))}
          </ul>
        ) : null}
      </fieldset>
    );
  };
}

/** The properties of an open chat: where it runs, its folder, its skills and MCP servers. */
export function ChatPropertiesDialog(handle: Handle<{ open: boolean; chat: Chat; adapters: AdapterInfo[]; onClose: () => void; onSaved: (c: Chat) => void }>) {
  const s = services(handle);
  const act = action(handle);
  const library = useSkillLibrary(handle, () => handle.props.open);
  const servers = useMcpLibrary(handle, () => handle.props.open);
  const conns = query(handle, {
    enabled: () => handle.props.open,
    when: (e) => e.type.startsWith('connection.'),
    load: async (): Promise<Connection[]> => {
      try {
        return await s.client.connectionList();
      } catch {
        return [];
      }
    },
  });
  let choice = '';
  let cwd = '';
  let skills: ChatSkillRef[] = [];
  let mcp: ChatMcpGrant[] = [];
  let wasOpen = false;
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };
  const current = (c: Chat) => (c.wslDistro ? `${c.adapter}@wsl:${c.wslDistro}` : c.adapter);

  return () => {
    const { open, chat, adapters, onClose, onSaved } = handle.props;
    if (open && !wasOpen) {
      choice = current(chat);
      cwd = chat.cwd;
      skills = [...(chat.skills ?? [])];
      mcp = (chat.mcpServers ?? []).map((g) => ({ ...g, tools: [...g.tools] }));
      if (act.error) handle.queueTask(() => act.clear());
    }
    wasOpen = open;
    const choices = chatChoices(adapters, conns.data ?? []);
    const picked = choices.find((c) => c.value === choice);
    const hasCurrent = choices.some((c) => c.value === current(chat));
    const moved = choice !== current(chat) || (!chat.projectId && cwd.trim() !== chat.cwd);

    const save = async () => {
      const u: ChatUpdate = { id: chat.id };
      if (choice !== current(chat) && picked) {
        u.adapter = picked.adapter.id;
        u.wslDistro = picked.wslDistro;
      }
      if (!chat.projectId && cwd.trim() !== chat.cwd) u.cwd = cwd.trim();
      if (!sameSkills(skills, chat.skills ?? [])) u.skills = skills;
      if (!sameServers(mcp, chat.mcpServers ?? [])) u.mcpServers = mcp;
      if (Object.keys(u).length === 1) {
        onClose();
        return;
      }
      const c = await act.run('save', () => s.client.chatUpdate(u));
      if (c) onSaved(c);
    };

    return (
      <Dialog open={open} onClose={onClose} title="Chat properties" label="Chat properties" wide>
        <form
          style={{ display: 'grid', gap: '12px', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' }}
          data-testid="chat-properties-form"
          mix={on('submit', (e) => {
            e.preventDefault();
            void save();
          })}
        >
          <Field label="Agent CLI" hint={picked?.wslDistro ? `Runs inside the WSL distro ${picked.wslDistro}, with the CLI installed and signed in there.` : undefined}>
            {(id) => (
              <select id={id} value={choice} mix={on('change', (e) => set(() => (choice = e.currentTarget.value)))}>
                {hasCurrent ? null : (
                  <option value={current(chat)} selected={choice === current(chat)}>
                    {chat.adapter}
                    {chat.wslDistro ? ` in ${chat.wslDistro} (WSL)` : ''} (current)
                  </option>
                )}
                {choices.map((c) => (
                  <option key={c.value} value={c.value} disabled={!!c.why && c.value !== current(chat)} selected={c.value === choice}>
                    {c.text}
                  </option>
                ))}
              </select>
            )}
          </Field>
          {chat.projectId ? (
            <Field label="Folder" hint="A chat in a project runs in the project's folder.">
              {(id) => <input id={id} value={chat.cwd} readOnly style={{ fontFamily: 'var(--font-mono)' }} />}
            </Field>
          ) : (
            <Field label="Folder" hint="An absolute path to an existing folder, checked when you save. Empty: a private folder Drydock makes for this chat.">
              {(id) => (
                <div mix={folderRow}>
                  <input id={id} value={cwd} spellcheck={false} autocomplete="off" style={{ fontFamily: 'var(--font-mono)' }} mix={on('input', (e) => set(() => (cwd = e.currentTarget.value)))} />
                  <ChooseFolder title="Choose the chat's folder" testId="chat-choose-folder" onPick={(path) => set(() => (cwd = path))} />
                </div>
              )}
            </Field>
          )}
          <SkillPicker library={library.data} loading={library.loading} error={library.error} chosen={skills} onChange={(v) => set(() => (skills = v))} />
          <ServerPicker servers={servers.data} loading={servers.loading} error={servers.error} chosen={mcp} had={chat.mcpServers ?? []} onChange={(v) => set(() => (mcp = v))} />
          <div style={{ gridColumn: '1 / -1' }}>
            <Notice tone={moved ? 'warn' : 'info'} testId="chat-properties-note">
              {moved
                ? 'A new agent CLI, place or folder cannot continue the current session: the next message starts a new one, given the conversation so far.'
                : 'Changes apply from the next message, which continues the same session.'}
            </Notice>
          </div>
          <div style={{ gridColumn: '1 / -1' }}>
            <ErrorNote error={act.error} what="Could not change the chat" />
          </div>
          <div mix={formActions} style={{ gridColumn: '1 / -1', justifyContent: 'flex-end' }}>
            <Button variant="ghost" mix={[on('click', () => onClose())]}>
              Cancel
            </Button>
            <Button variant="primary" type="submit" disabled={!!act.busy}>
              {act.busy === 'save' ? 'Saving...' : 'Save'}
            </Button>
          </div>
        </form>
      </Dialog>
    );
  };
}

/** Calls of this chat's MCP tools that wait for the person (a tool not marked read-only), with Approve and Reject. */
export function ChatApprovals(handle: Handle<{ chat: Chat }>) {
  const s = services(handle);
  const act = action(handle);
  const list = query(handle, {
    enabled: () => (handle.props.chat.mcpServers?.length ?? 0) > 0,
    key: () => handle.props.chat.id,
    when: (e) => e.type.startsWith('approval.') || e.type.startsWith('mcp.'),
    load: async (): Promise<McpApproval[]> => (await s.client.mcpApprovalList(handle.props.chat.projectId)).filter((a) => a.agentId === handle.props.chat.id),
  });
  const decide = async (id: string, approve: boolean) => {
    await act.run(`${approve ? 'approve' : 'reject'}:${id}`, () => s.client.mcpApprovalDecide(id, approve));
    void list.reload();
  };
  return () => {
    const items = (handle.props.chat.mcpServers?.length ?? 0) > 0 ? (list.data ?? []) : [];
    if (!items.length && !act.error) return null;
    return (
      <div style={{ display: 'grid', gap: '6px', padding: '8px 20px 0' }} data-testid="chat-mcp-approvals">
        {items.map((a) => (
          <Notice key={a.id} tone="warn" role="alert" testId="chat-mcp-approval" title={`The agent wants to call ${a.server}: ${a.tool}`}>
            <code style={{ display: 'block', overflowWrap: 'anywhere', whiteSpace: 'pre-wrap' }}>{a.args}</code>
            <span style={{ display: 'inline-flex', gap: '6px', marginTop: '6px' }}>
              <Button size="sm" variant="primary" disabled={!!act.busy} mix={[on('click', () => void decide(a.id, true))]}>
                Approve this call
              </Button>
              <Button size="sm" variant="secondary" disabled={!!act.busy} mix={[on('click', () => void decide(a.id, false))]}>
                Reject
              </Button>
            </span>
          </Notice>
        ))}
        <ErrorNote error={act.error} />
      </div>
    );
  };
}
