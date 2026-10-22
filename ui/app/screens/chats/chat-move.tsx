// "Move to a project" (decision P97): a chat moves into a project (or out of the one it is in) with chat.update projectId,
// only while no turn runs. The dialog lists the projects and says what happens to the folder, as the core does it: in a
// project the chat runs in the project's folder, and when that is another folder the next message starts a new session given
// the conversation so far; taken out of a project it keeps the folder it ran in, and its session. Its allow rules, skills and
// MCP servers are its own and stay.

import { on, type Handle } from 'remix/component';
import type { Chat } from '../../api/chatClient.ts';
import type { Project } from '../../api/types.ts';
import { action, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { Dialog } from '../../ui/dialog.tsx';
import { ErrorNote, Notice } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { Select } from '../../ui/select.tsx';

/** The choice that takes the chat out of its project. */
export const OUT_OF_PROJECT = '__none';

/** What moving the chat does to its folder and session, in words (chats.Service.Update, docs/chats.md). */
export function moveEffect(c: Pick<Chat, 'projectId'>, to: string, projectName: string): string {
  if (to === OUT_OF_PROJECT) return 'It leaves the project and keeps the folder it runs in, so its session continues.';
  const into = c.projectId ? `It moves to ${projectName}` : `It joins ${projectName}`;
  return `${into} and runs in the project's folder. When that is another folder, the next message starts a new session, given the conversation so far.`;
}

export function MoveChatDialog(handle: Handle<{ open: boolean; chat: Chat; projects: readonly Project[]; running: boolean; onClose: () => void; onMoved: (c: Chat) => void }>) {
  const s = services(handle);
  const act = action(handle);
  let to = '';
  let wasOpen = false;
  /** The last refusal belongs to an earlier opening of the dialog: not shown. */
  let stale = false;
  return () => {
    const { open, chat, projects, running, onClose, onMoved } = handle.props;
    const others = projects.filter((p) => p.id !== chat.projectId);
    if (open && !wasOpen) {
      // each opening starts from the first choice, without the last refusal
      to = others[0]?.id ?? (chat.projectId ? OUT_OF_PROJECT : '');
      stale = true;
    }
    wasOpen = open;
    const options = [...others.map((p) => ({ value: p.id, label: p.name })), ...(chat.projectId ? [{ value: OUT_OF_PROJECT, label: 'No project' }] : [])];
    const name = projects.find((p) => p.id === to)?.name ?? 'the project';
    const move = async () => {
      if (!to || running) return;
      stale = false;
      const c = await act.run('move', () => s.client.chatUpdate({ id: chat.id, projectId: to === OUT_OF_PROJECT ? '' : to }));
      if (c) onMoved(c);
    };
    return (
      <Dialog open={open} onClose={onClose} title={chat.projectId ? 'Move to another project' : 'Move to a project'}>
        <form
          style={{ display: 'grid', gap: '16px' }}
          data-testid="chat-move-form"
          mix={on('submit', (e) => {
            e.preventDefault();
            void move();
          })}
        >
          {options.length ? (
            <>
              <Field label="Project">{(id) => <Select id={id} value={to} options={options} onChange={(v) => ((to = v), void handle.update())} />}</Field>
              <p style={{ margin: '0' }} data-testid="chat-move-effect">
                {moveEffect(chat, to, name)} Its allow rules, skills and MCP servers stay.
              </p>
            </>
          ) : (
            <p style={{ margin: '0' }}>There is no project to move it to yet.</p>
          )}
          {running ? <Notice tone="warn">Stop the running turn first: a chat moves only between turns.</Notice> : null}
          <ErrorNote error={stale ? undefined : act.error} what="Could not move the chat" />
          <div mix={formActions} style={{ justifyContent: 'flex-end' }}>
            <Button variant="ghost" mix={[on('click', () => onClose())]}>
              Cancel
            </Button>
            <Button variant="primary" type="submit" disabled={!to || running || !!act.busy}>
              {act.busy ? 'Moving...' : 'Move'}
            </Button>
          </div>
        </form>
      </Dialog>
    );
  };
}
