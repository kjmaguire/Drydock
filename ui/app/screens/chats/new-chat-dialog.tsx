// The new chat dialog: where it runs (the current project by default, or no project with an optional folder), which agent
// CLI (Claude Code by default; one that is not installed is listed but disabled with the reason; a CLI installed inside a WSL
// distro is its own choice, as on the Connections page, and the chat then runs there: decision P41), model, effort, permission
// mode (a mode that skips approvals shows a warning) and whether the chat may read the human's other chats.
// It also takes the chat's properties (decision P60): skills from the workspace skill library and MCP servers from the
// workspace MCP library, which reach the chat's CLI through Drydock's MCP gateway.

import { css, on, type Handle } from 'remix/component';
import type { AdapterInfo, Connection, Project } from '../../api/types.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox, Field, formActions, formGrid } from '../../ui/form.tsx';
import { Select, selectValues } from '../../ui/select.tsx';
import { ModelField } from '../shared/model-field.tsx';
import type { ChatLastChoice, ChatMcpGrant, ChatSkillRef } from '../../api/chatClient.ts';
import { ServerPicker, SkillPicker, useMcpLibrary, useSkillLibrary } from './chat-properties.tsx';
import { chatChoices, pickChatChoice, skipsApprovals } from './chats.ts';
import { ChooseFolder, folderRow } from '../shared/choose-folder.tsx';
import { noteFromLast } from './from-last.ts';
import { CliFixButton } from '../shared/cli-fix.tsx';
import { t } from '../../ui/tokens.ts';
import { adapterLabel } from '../shared/cli-name.ts';

// a CLI listed that cannot be used yet, with its fix (A9)
const blockedCss = css({ display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', gridColumn: '1 / -1', '& > span': { color: t.textMuted, fontSize: t.textSm } });

const NO_PROJECT = '';
const FROM_LAST = 'From your last chat.';
const noChoice: ChatLastChoice = { model: '', effort: '', permissionMode: '' };

export function NewChatDialog(
  handle: Handle<{ open: boolean; onClose: () => void; projects: Project[]; projectId: string | undefined; adapters: AdapterInfo[] | undefined; adaptersError?: Error; onCreated: (chatId: string) => void }>,
) {
  const s = services(handle);
  const act = action(handle);
  // the WSL installs are the WSL Connections (each distro probed): read while the dialog is open, and again when a sign-in or
  // a gate changes one. A core without them offers only the Windows installs.
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
  // what a chat of the project and CLI starts with when a field is left alone (chat.lastChoice, D2): the fields show it, so
  // what the dialog shows is what is created. Read for the project and CLI picked; an older core carries nothing.
  const lastKey = () => `${project ?? handle.props.projectId ?? NO_PROJECT}\n${pickChatChoice(chatChoices(handle.props.adapters ?? [], conns.data ?? []), adapter)?.adapter.id ?? ''}`;
  const last = query(handle, {
    key: lastKey,
    enabled: () => handle.props.open && !lastKey().endsWith('\n'),
    load: async (): Promise<{ key: string; choice: ChatLastChoice }> => {
      const key = lastKey();
      const [pid, id] = key.split('\n') as [string, string];
      try {
        return { key, choice: await s.client.chatLastChoice(pid, id) };
      } catch {
        return { key, choice: noChoice };
      }
    },
  });
  let project: string | undefined; // undefined = the current project
  let cwd = '';
  let adapter = ''; // the picked choice's value: an adapter id, or a WSL Connection key ("claude@wsl:Ubuntu")
  let model = '';
  let effort = '';
  let mode = '';
  let readHistory = true;
  // the chat's properties (decision P60): library skills and MCP servers, read while the dialog is open
  let skills: ChatSkillRef[] = [];
  let servers: ChatMcpGrant[] = [];
  const library = useSkillLibrary(handle, () => handle.props.open);
  const mcpLib = useMcpLibrary(handle, () => handle.props.open);
  let wasOpen = false;

  const reset = () => {
    project = undefined;
    cwd = '';
    adapter = '';
    model = '';
    effort = '';
    mode = '';
    readHistory = true;
    skills = [];
    servers = [];
    if (act.error) handle.queueTask(() => act.clear()); // not during render
  };
  const set = (fn: () => void) => {
    fn();
    void handle.update();
  };

  return () => {
    const { open, onClose, projects, projectId, adapters, adaptersError } = handle.props;
    if (open && !wasOpen) reset();
    wasOpen = open;
    const choices = chatChoices(adapters ?? [], conns.data ?? []);
    const chosen = pickChatChoice(choices, adapter);
    // the first CLI listed that cannot be used yet, offered with its fix
    // the CLI chosen is not signed in (it can still be chosen: a turn would be refused), or the first listed that cannot be used
    const firstBlocked = choices.find((c) => !!c.why && c.adapter.detection.installed) ?? (chosen ? undefined : choices.find((c) => !!c.why));
    const blocked =
      chosen && !chosen.wslDistro && chosen.adapter.detection.status === 'unauthenticated'
        ? { adapter: chosen.adapter, wslDistro: '', text: `${adapterLabel(chosen.adapter.id)} is not signed in.` }
        : firstBlocked;
    const caps = chosen?.adapter.capabilities;
    const distro = chosen?.wslDistro ?? '';
    const models = caps?.models ?? [];
    const efforts = caps?.efforts ?? [];
    const modes = caps?.permissionModes ?? [];
    const pid = project ?? projectId ?? NO_PROJECT;
    // the last choice for this project and CLI, once read for them (until then Start chat waits, so nothing differs from the form)
    const lastRead = chosen && last.data?.key === `${pid}\n${chosen.adapter.id}` ? last.data.choice : undefined;
    const carried = lastRead ?? noChoice;
    // a field left empty takes the last choice (the core fills it, chats/prefs.go), so it shows it
    const modelNow = model || carried.model;
    // the core keeps the last effort only for a model it suits: with another model picked the effort shown is sent as it is
    const effortNow = effort || carried.effort;
    const modeNow = mode || carried.permissionMode || (modes.includes('default') ? 'default' : '');
    const fromLast = (picked: string, was: string) => !picked && !!was;
    // the folder is checked by the core (it knows its own system's paths): a refusal shows as "Could not start the chat"
    const canCreate = !!chosen && !!lastRead && !act.busy;

    const create = async () => {
      if (!canCreate || !chosen) return;
      const c = await act.run('create', () =>
        s.client.chatCreate({
          projectId: pid || undefined,
          cwd: pid ? undefined : cwd.trim() || undefined,
          adapter: chosen.adapter.id,
          ...(chosen.wslDistro ? { wslDistro: chosen.wslDistro } : {}),
          model: model.trim() || undefined,
          // read when sent: the model may have changed since this render (the last choice is fixed once read)
          effort: (model && model !== carried.model ? effort || carried.effort : effort) || undefined,
          permissionMode: mode || undefined,
          readHistory,
          ...(skills.length ? { skills } : {}),
          ...(servers.length ? { mcpServers: servers } : {}),
        }),
      );
      // the screen closes the dialog and navigates once (closing first would race a second navigation)
      if (c) {
        noteFromLast(c);
        handle.props.onCreated(c.id);
      }
    };

    return (
      <Dialog open={open} onClose={onClose} title="New chat" label="New chat" wide>
        {!adapters && !adaptersError ? (
          <Loading label="Looking for agent CLIs..." />
        ) : (
          <form
            mix={[
              formGrid,
              on('submit', (e) => {
                e.preventDefault();
                void create();
              }),
            ]}
            data-testid="new-chat-form"
            style={{ gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' }}
          >
            <Field label="Project" hint={pid ? 'The chat runs in the project folder.' : 'The chat is not tied to a project.'}>
              {(id) => (
                <Select
                  id={id}
                  value={pid}
                  options={[...projects.map((p) => ({ value: p.id, label: p.name })), { value: NO_PROJECT, label: 'No project' }]}
                  onChange={(v) => set(() => (project = v))}
                />
              )}
            </Field>
            {pid === NO_PROJECT ? (
              <Field
                label="Folder"
                hint={
                  distro
                    ? `An existing folder inside ${distro} (/home/you/notes) or on Windows (C:\\work\\notes), checked when the chat starts. Empty: a private folder in your home inside ${distro} (~/.drydock/chats).`
                    : 'An absolute path to an existing folder, checked when the chat starts. Empty: a private folder Drydock creates for this chat.'
                }
              >
                {(id) => (
                  <div mix={folderRow}>
                    <input id={id} value={cwd} placeholder={distro ? '/home/you/notes' : 'C:\\work\\notes'} spellcheck={false} autocomplete="off" style={{ fontFamily: 'var(--font-mono)' }} mix={on('input', (e) => set(() => (cwd = e.currentTarget.value)))} />
                    <ChooseFolder title="Choose the chat's folder" testId="chat-choose-folder" onPick={(path) => set(() => (cwd = path))} />
                  </div>
                )}
              </Field>
            ) : (
              <div />
            )}
            <Field
              label="Agent CLI"
              hint={
                distro
                  ? `Runs inside the WSL distro ${distro}, with the Claude Code installed and signed in there.`
                  : chosen
                    ? conns.loading && !conns.data
                      ? 'Looking for agent CLIs inside WSL...'
                      : undefined
                    : 'No agent CLI is installed. Set one up on the Connections page.'
              }
            >
              {(id) => (
                <Select
                  id={id}
                  value={chosen?.value ?? ''}
                  options={choices.map((c) => ({ value: c.value, label: c.text, disabled: !!c.why }))}
                  placeholder="No agent CLI is installed"
                  onChange={(v) => set(() => ((adapter = v), (model = ''), (effort = ''), (mode = '')))}
                />
              )}
            </Field>
            {/* a CLI listed but not usable yet (not signed in, a notice, not installed): fixed in place (A9) */}
            {blocked ? (
              <div mix={blockedCss} data-testid="new-chat-blocked">
                <span>{blocked.text}</span>
                <CliFixButton adapter={blocked.adapter.id} distro={blocked.wslDistro || undefined} testId="new-chat-dialog-fix" />
              </div>
            ) : null}
            <Field label="Model" hint={fromLast(model, carried.model) ? FROM_LAST : undefined}>
              {(id) => (
                <ModelField
                  id={id}
                  value={models.length ? modelNow : model}
                  models={models}
                  // with a last choice, an empty model takes it: no "CLI default" to pick that would not be kept
                  emptyLabel={carried.model ? undefined : 'CLI default'}
                  placeholder={carried.model || 'CLI default'}
                  disabled={caps?.modelSelection === false && models.length === 0}
                  onChange={(v) => set(() => (model = v))}
                />
              )}
            </Field>
            <Field label="Effort" hint={fromLast(effort, carried.effort) ? FROM_LAST : undefined}>
              {(id) => (
                <Select
                  id={id}
                  value={effortNow}
                  disabled={efforts.length === 0}
                  options={[...(carried.effort ? [] : [{ value: '', label: 'CLI default' }]), ...selectValues(efforts)]}
                  onChange={(v) => set(() => (effort = v))}
                />
              )}
            </Field>
            <Field
              label="Permission mode"
              hint={
                fromLast(mode, carried.permissionMode)
                  ? FROM_LAST
                  : chosen?.adapter.id === 'claude' && modeNow === 'default'
                    ? 'Tools outside the mode are refused and shown, with a rule you can allow.'
                    : undefined
              }
            >
              {(id) => (
                <Select
                  id={id}
                  value={modeNow}
                  disabled={modes.length === 0}
                  options={modes.length === 0 ? [{ value: '', label: 'CLI default' }] : modes.map((x) => ({ value: x, label: `${x}${skipsApprovals(x) ? ' (skips approvals)' : ''}` }))}
                  onChange={(v) => set(() => (mode = v))}
                />
              )}
            </Field>
            {skipsApprovals(modeNow) ? (
              <div style={{ gridColumn: '1 / -1' }}>
                <Notice tone="warn" role="alert" testId="skip-warning" title={`"${modeNow}" skips approvals.`}>
                  The agent will run tools (shell commands, file edits) without asking you. Pick it only for a folder you can afford to lose changes in.
                </Notice>
              </div>
            ) : null}
            <div style={{ gridColumn: '1 / -1' }}>
              <Checkbox
                label="Let this chat read my other chats"
                description={pid ? "A read-only tool to list, search and read this project's chats and your chats without a project." : 'A read-only tool to list, search and read every chat.'}
                checked={readHistory}
                onChange={(v) => set(() => (readHistory = v))}
              />
            </div>
            <SkillPicker library={library.data} loading={library.loading} error={library.error} chosen={skills} onChange={(v) => set(() => (skills = v))} />
            <ServerPicker servers={mcpLib.data} loading={mcpLib.loading} error={mcpLib.error} chosen={servers} onChange={(v) => set(() => (servers = v))} />
            <div style={{ gridColumn: '1 / -1' }}>
              <ErrorNote error={adaptersError} what="Could not list the agent CLIs" />
              <ErrorNote error={act.error} what="Could not start the chat" />
            </div>
            <div mix={formActions} style={{ justifyContent: 'flex-end' }}>
              <Button variant="ghost" mix={[on('click', () => onClose())]}>
                Cancel
              </Button>
              <Button variant="primary" type="submit" disabled={!canCreate}>
                {act.busy === 'create' ? 'Starting...' : 'Start chat'}
              </Button>
            </div>
          </form>
        )}
      </Dialog>
    );
  };
}
