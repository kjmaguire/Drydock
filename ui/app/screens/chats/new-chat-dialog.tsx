// The new chat dialog: where it runs (the current project by default, or no project with an optional folder), which agent
// CLI (Claude Code by default; one that is not installed is listed but disabled with the reason), model, effort, permission
// mode (a mode that skips approvals shows a warning) and whether the chat may read the human's other chats.

import { on, type Handle } from 'remix/component';
import type { AdapterInfo, Project } from '../../api/types.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Loading, Notice } from '../../ui/feedback.tsx';
import { Checkbox, Field, formActions, formGrid } from '../../ui/form.tsx';
import { adapterOptionText, adapterUnavailable, isAbsolutePath, skipsApprovals, sortChatAdapters } from './chats.ts';

const NO_PROJECT = '';

export function NewChatDialog(
  handle: Handle<{ open: boolean; onClose: () => void; projects: Project[]; projectId: string | undefined; adapters: AdapterInfo[] | undefined; adaptersError?: Error; onCreated: (chatId: string) => void }>,
) {
  const s = services(handle);
  const act = action(handle);
  let project: string | undefined; // undefined = the current project
  let cwd = '';
  let adapter = '';
  let model = '';
  let effort = '';
  let mode = '';
  let readHistory = true;
  let tried = false;
  let wasOpen = false;
  const listId = `new-chat-models-${handle.id}`;

  const reset = () => {
    project = undefined;
    cwd = '';
    adapter = '';
    model = '';
    effort = '';
    mode = '';
    readHistory = true;
    tried = false;
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
    const list = sortChatAdapters(adapters ?? []);
    const usable = list.filter((a) => !adapterUnavailable(a));
    const chosen = list.find((a) => a.id === adapter && !adapterUnavailable(a)) ?? usable.find((a) => a.id === 'claude') ?? usable[0];
    const caps = chosen?.capabilities;
    const models = caps?.models ?? [];
    const efforts = caps?.efforts ?? [];
    const modes = caps?.permissionModes ?? [];
    const pid = project ?? projectId ?? NO_PROJECT;
    const modeNow = mode || (modes.includes('default') ? 'default' : '');
    const cwdError = pid === NO_PROJECT && cwd.trim() && !isAbsolutePath(cwd) ? 'Enter an absolute folder path, such as C:\\work\\notes (or leave it empty for a private folder).' : '';
    const canCreate = !!chosen && !cwdError && !act.busy;

    const create = async () => {
      tried = true;
      if (!canCreate || !chosen) {
        void handle.update();
        return;
      }
      const c = await act.run('create', () =>
        s.client.chatCreate({
          projectId: pid || undefined,
          cwd: pid ? undefined : cwd.trim() || undefined,
          adapter: chosen.id,
          model: model.trim() || undefined,
          effort: effort || undefined,
          permissionMode: mode || undefined,
          readHistory,
        }),
      );
      // the screen closes the dialog and navigates once (closing first would race a second navigation)
      if (c) handle.props.onCreated(c.id);
    };

    return (
      <Dialog open={open} onClose={onClose} title="New chat" label="New chat">
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
                <select id={id} value={pid} mix={on('change', (e) => set(() => (project = e.currentTarget.value)))}>
                  {projects.map((p) => (
                    <option key={p.id} value={p.id} selected={p.id === pid}>
                      {p.name}
                    </option>
                  ))}
                  <option value={NO_PROJECT} selected={pid === NO_PROJECT}>
                    No project
                  </option>
                </select>
              )}
            </Field>
            {pid === NO_PROJECT ? (
              <Field label="Folder" error={tried || cwd ? cwdError : undefined} hint="An absolute path to an existing folder. Empty: a private folder Drydock creates for this chat.">
                {(id) => <input id={id} value={cwd} placeholder="C:\work\notes" spellcheck={false} autocomplete="off" style={{ fontFamily: 'var(--font-mono)' }} mix={on('input', (e) => set(() => (cwd = e.currentTarget.value)))} />}
              </Field>
            ) : (
              <div />
            )}
            <Field label="Agent CLI" hint={chosen ? undefined : 'No agent CLI is installed. Set one up on the Connections page.'}>
              {(id) => (
                <select id={id} value={chosen?.id ?? ''} mix={on('change', (e) => set(() => ((adapter = e.currentTarget.value), (model = ''), (effort = ''), (mode = ''))))}>
                  {list.map((a) => (
                    <option key={a.id} value={a.id} disabled={!!adapterUnavailable(a)} selected={a.id === chosen?.id}>
                      {adapterOptionText(a)}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Field label="Model">
              {(id) => (
                <>
                  <input
                    id={id}
                    value={model}
                    list={models.length ? listId : undefined}
                    disabled={caps?.modelSelection === false && models.length === 0}
                    placeholder="adapter default"
                    spellcheck={false}
                    autocomplete="off"
                    mix={on('input', (e) => void (model = e.currentTarget.value))}
                  />
                  {models.length ? (
                    <datalist id={listId}>
                      {models.map((m) => (
                        <option key={m} value={m} />
                      ))}
                    </datalist>
                  ) : null}
                </>
              )}
            </Field>
            <Field label="Effort">
              {(id) => (
                <select id={id} value={effort} disabled={efforts.length === 0} mix={on('change', (e) => set(() => (effort = e.currentTarget.value)))}>
                  <option value="" selected={effort === ''}>
                    adapter default
                  </option>
                  {efforts.map((x) => (
                    <option key={x} value={x} selected={x === effort}>
                      {x}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <Field label="Permission mode" hint={chosen?.id === 'claude' && modeNow === 'default' ? 'Tools outside the mode are refused and shown, with a rule you can allow.' : undefined}>
              {(id) => (
                <select id={id} value={modeNow} disabled={modes.length === 0} mix={on('change', (e) => set(() => (mode = e.currentTarget.value)))}>
                  {modes.length === 0 ? (
                    <option value="" selected>
                      agent default
                    </option>
                  ) : null}
                  {modes.map((x) => (
                    <option key={x} value={x} selected={x === modeNow}>
                      {x}
                      {skipsApprovals(x) ? ' (skips approvals)' : ''}
                    </option>
                  ))}
                </select>
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
