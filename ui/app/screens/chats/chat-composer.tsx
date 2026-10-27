// The chat composer (graphite, decision P97): one box with the message field (Enter sends, Shift+Enter is a new line, Ctrl+Enter
// sends too) and a row of small buttons under it: the chat's own controls (the model, the approval mode and "Move to a
// project", given by the conversation), attach files, attach chats (a picker of other chats; their history is given to the
// agent for this one message, shown as removable chips), then Stop while a turn runs and "↵ send". A send while a turn runs is
// not possible (the core refuses it too: one turn at a time).

import { css, on, ref, type Handle, type RemixNode } from 'remix/component';
import { FileText, MessagesSquare, Paperclip, Square, X } from 'lucide';
import type { Chat, ChatListItem } from '../../api/chatClient.ts';
import { query, services } from '../../state/services.tsx';
import { Icon } from '../../ui/icon.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Popover } from '../../ui/popover.tsx';
import { t } from '../../ui/tokens.ts';
import { checkFiles, formatSize, MAX_FILES } from '../room/messages.ts';
import { adapterLabel, agentWord, chatTitle, MAX_ATTACH, relTime } from './chats.ts';

const wrapCss = css({ flex: 'none', width: 'calc(100% - 48px)', maxWidth: '812px', margin: `0 auto 20px`, boxSizing: 'border-box' });
const boxCss = css({
  display: 'grid',
  gap: '8px',
  padding: '12px',
  border: `1px solid ${t.border}`,
  borderRadius: '12px',
  background: t.bgSurface,
  transition: `border-color ${t.fast} ${t.ease}, box-shadow ${t.fast} ${t.ease}`,
  '&:focus-within': { borderColor: t.borderStrong },
  '&[data-disabled="true"]': { opacity: '0.7' },
  '& textarea': {
    display: 'block',
    width: '100%',
    minHeight: '28px',
    maxHeight: '40vh',
    padding: '4px 2px',
    border: '0',
    borderRadius: '0',
    background: 'transparent',
    resize: 'none',
    fontSize: t.textMd,
    lineHeight: '1.5',
    boxShadow: 'none',
  },
  '& textarea:focus-visible': { boxShadow: 'none', outline: 'none' },
});
const barCss = css({ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: '8px', minWidth: '0' });
/** A small button of the composer's row (the model, the approval mode, the attach buttons). */
export const smallBtn = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '5px',
  height: '28px',
  minHeight: '0',
  maxWidth: '100%',
  padding: '0 10px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgRaised,
  color: t.textMuted,
  fontSize: t.textXs,
  fontWeight: '500',
  whiteSpace: 'nowrap',
  '& > span': { overflow: 'hidden', textOverflow: 'ellipsis' },
  '&:hover': { background: t.bgHover, color: t.text },
  '&[aria-expanded="true"]': { background: t.bgActive, color: t.text },
  '&:disabled': { opacity: '0.5', pointerEvents: 'none' },
  '&[data-warn="true"]': { borderColor: `color-mix(in srgb, ${t.warning} 50%, ${t.border})`, color: t.warning, background: t.warningSoft },
});
/** The same, without a border (Move to a project, the attach buttons). */
export const quietBtn = css({ borderColor: 'transparent', background: 'transparent' });
const iconBtn = css({ width: '28px', padding: '0', justifyContent: 'center' });
const stopCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  height: '28px',
  minHeight: '0',
  padding: '0 10px',
  border: `1px solid ${t.borderStrong}`,
  borderRadius: t.rMd,
  background: t.bgRaised,
  color: t.text,
  fontSize: t.textXs,
  fontWeight: '600',
  '&:hover': { background: t.dangerSoft, color: t.danger, borderColor: 'transparent' },
});
// "↵ send" is the send button itself: its name stays "Send" for readers
const sendCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '4px',
  height: '28px',
  minHeight: '0',
  padding: '0 6px',
  border: '0',
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.textMuted,
  fontSize: t.textXs,
  '&:hover': { background: t.bgHover, color: t.text },
  '&:disabled': { color: t.textFaint, opacity: '1', background: 'transparent' },
});
/** A file read in the composer, waiting to be sent. */
export interface ComposerFile {
  name: string;
  size: number;
  data: Uint8Array;
}

const chipsCss = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '6px', margin: '0', padding: '0', listStyle: 'none' });
const chipCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '5px',
  maxWidth: '260px',
  height: '28px',
  padding: '0 2px 0 8px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgRaised,
  fontSize: t.textSm,
  '& .name': { whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', fontWeight: '500' },
  '& button': { display: 'grid', placeItems: 'center', width: '24px', height: '24px', minHeight: '0', padding: '0', border: '0', borderRadius: t.rSm, background: 'transparent', color: t.textMuted },
  '& button:hover': { background: t.bgHover, color: t.text },
});
const explainCss = css({ color: t.textFaint, fontSize: t.textXs });
const pickList = css({
  display: 'grid',
  gap: '1px',
  margin: '0',
  padding: '0',
  listStyle: 'none',
  maxHeight: '280px',
  overflowY: 'auto',
  '& button': {
    display: 'grid',
    gridTemplateColumns: '16px minmax(0, 1fr) auto',
    alignItems: 'center',
    gap: t.s2,
    width: '100%',
    minHeight: '28px',
    padding: '6px 8px',
    border: '0',
    borderRadius: t.rMd,
    background: 'transparent',
    color: t.text,
    textAlign: 'left',
    fontSize: t.textSm,
  },
  '& button:hover': { background: t.bgHover },
  '& button[aria-pressed="true"]': { background: t.accentSoft },
  '& button:disabled': { opacity: '0.5' },
  '& .t': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: '500' },
  '& .w': { color: t.textFaint, fontSize: t.textXs },
  '& input': { margin: '0', pointerEvents: 'none' },
});
const noteCss = css({ margin: '0', color: t.textMuted, fontSize: t.textSm });

/** How many chats the attach picker offers (the most recent, or the best matches of its search). */
const ATTACH_PICKER_CHATS = 100;
const FILTER_DEBOUNCE_MS = 250;

/** Whether a key press in the message field sends: Enter (not while an input method composes, not with Shift), or Ctrl+Enter. */
export function sendsOnKey(k: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'altKey' | 'isComposing'>): boolean {
  return k.key === 'Enter' && !k.shiftKey && !k.altKey && !k.isComposing;
}

export function ChatComposer(
  handle: Handle<{
    chat: Chat;
    running: boolean;
    disabled: boolean;
    disabledReason: string;
    sending: boolean;
    stopping: boolean;
    /** No message yet: the field asks for the first one rather than a reply. */
    empty?: boolean;
    /** The chat's own controls at the start of the row (the model, the approval mode, Move to a project). */
    controls?: RemixNode;
    onSend: (text: string, attach: string[], files: ComposerFile[]) => Promise<boolean>;
    onStop: () => void;
  }>,
) {
  const s = services(handle);
  let text = '';
  let attach: string[] = [];
  // files sent with the message (P90): read when added, so a send has them at hand
  let files: ComposerFile[] = [];
  let fileError: Error | undefined;
  let fileInput: HTMLInputElement | undefined;
  const addFiles = async (list: FileList | File[]) => {
    fileError = undefined;
    const incoming = Array.from(list);
    const bad = checkFiles(files.length, incoming);
    if (bad) {
      fileError = new Error(bad);
      void handle.update();
      return;
    }
    const read: ComposerFile[] = [];
    for (const f of incoming) read.push({ name: f.name, size: f.size, data: new Uint8Array(await f.arrayBuffer()) });
    // checked again against the files added while these were read (two drops at once must not pass the limit together)
    const late = checkFiles(files.length, incoming);
    if (late) {
      fileError = new Error(late);
      void handle.update();
      return;
    }
    files = [...files, ...read];
    void handle.update();
  };
  let picking = false;
  let filter = '';
  /** The filter as searched (debounced): chat.list's full-text query, so any chat can be found, not only the recent ones. */
  let search = '';
  let timer: ReturnType<typeof setTimeout> | undefined;
  handle.signal.addEventListener('abort', () => clearTimeout(timer));
  /** The title of each attached chat, from when it was picked: a chip keeps it when the chat leaves the list shown. */
  const titles = new Map<string, string>();
  let area: HTMLTextAreaElement | undefined;
  // the other chats of every project (attaching one from another project is the human's choice), read when the picker opens:
  // the most recent ones, or with a filter the ones chat.list's search finds (archived chats included)
  const others = query(handle, {
    load: () => (search ? s.client.chatList({ query: search, includeArchived: true, limit: ATTACH_PICKER_CHATS }) : s.client.chatList({ limit: ATTACH_PICKER_CHATS })),
    when: (e) => e.type === 'chat.created' || e.type === 'chat.deleted' || e.type === 'chat.updated',
    key: () => search,
    enabled: () => picking || attach.length > 0,
    debounceMs: 400,
  });

  const onFilter = (v: string) => {
    filter = v;
    clearTimeout(timer);
    timer = setTimeout(() => {
      search = filter.trim();
      void handle.update();
    }, FILTER_DEBOUNCE_MS);
    void handle.update();
  };

  const canSend = () => !handle.props.disabled && !handle.props.running && !handle.props.sending && text.trim().length > 0 && files.length <= MAX_FILES;

  const submit = async () => {
    if (!canSend()) return;
    // what is sent leaves the box at once, so what the person types, attaches or drops while it is on its way is theirs alone;
    // a send that fails puts it back in front (review 2026-10-27: clearing on the answer left an edited message to send twice)
    const sent = { text, attach, files };
    text = '';
    attach = [];
    files = [];
    fileError = undefined;
    show();
    const ok = await handle.props.onSend(sent.text, sent.attach, sent.files);
    if (!ok) {
      text = text.trim() ? `${sent.text}\n${text}` : sent.text;
      attach = [...sent.attach, ...attach.filter((id) => !sent.attach.includes(id))].slice(0, MAX_ATTACH);
      files = [...sent.files, ...files];
      // files dropped meanwhile can take the two over the limit: all are kept and the person removes some before sending again
      if (files.length > MAX_FILES) fileError = new Error(checkFiles(0, files));
      show();
    }
  };

  const show = () => {
    if (area) {
      area.value = text;
      area.style.height = '';
      if (text) fit();
    }
    void handle.update();
  };

  const fit = () => {
    if (!area) return;
    area.style.height = 'auto';
    area.style.height = `${Math.min(area.scrollHeight, Math.round(window.innerHeight * 0.4))}px`;
  };

  const pick = (c: ChatListItem) => {
    titles.set(c.id, chatTitle(c));
    toggle(c.id);
  };

  const toggle = (id: string) => {
    attach = attach.includes(id) ? attach.filter((x) => x !== id) : attach.length < MAX_ATTACH ? [...attach, id] : attach;
    void handle.update();
  };

  return () => {
    const { chat, running, disabled, disabledReason, sending, stopping, empty, controls, onStop } = handle.props;
    const list: ChatListItem[] = (others.data ?? []).filter((c) => c.id !== chat.id);
    const titleOf = (id: string) => {
      const c = list.find((x) => x.id === id);
      return titles.get(id) ?? (c ? chatTitle(c) : id);
    };
    const who = adapterLabel(chat.adapter);
    return (
      <form
        mix={[
          wrapCss,
          on('submit', (e) => {
            e.preventDefault();
            void submit();
          }),
        ]}
        aria-label="Send a message"
        data-testid="chat-composer"
      >
        <div
          mix={[
            boxCss,
            on('dragover', (e) => {
              if (!disabled) e.preventDefault();
            }),
            on('drop', (e) => {
              const fs = (e as DragEvent).dataTransfer?.files;
              if (fs && fs.length && !disabled) {
                e.preventDefault();
                void addFiles(fs);
              }
            }),
          ]}
          data-disabled={disabled ? 'true' : undefined}
        >
          {files.length ? (
            <ul mix={chipsCss} aria-label="Attached files">
              {files.map((f, i) => (
                <li key={`${i}:${f.name}`} mix={chipCss} data-testid="file-chip">
                  <Icon icon={FileText} size={12} />
                  <span class="name">{f.name}</span>
                  <span>{formatSize(f.size)}</span>
                  <button
                    type="button"
                    aria-label={`Remove attached file ${f.name}`}
                    mix={on('click', () => {
                      files = files.filter((_, j) => j !== i);
                      if (files.length <= MAX_FILES) fileError = undefined;
                      void handle.update();
                    })}
                  >
                    <Icon icon={X} size={12} />
                  </button>
                </li>
              ))}
              <li mix={explainCss}>A text file is quoted to {agentWord(chat.adapter)} as data; a picture goes only to an agent that takes pictures.</li>
            </ul>
          ) : null}
          {fileError ? <ErrorNote error={fileError} /> : null}
          {attach.length ? (
            <div>
              <ul mix={chipsCss} aria-label="Attached chats">
                {attach.map((id) => (
                  <li key={id} mix={chipCss} data-testid="attach-chip">
                    <Icon icon={MessagesSquare} size={12} />
                    <span class="name">{titleOf(id)}</span>
                    <button type="button" aria-label={`Remove attached chat ${titleOf(id)}`} mix={on('click', () => toggle(id))}>
                      <Icon icon={X} size={12} />
                    </button>
                  </li>
                ))}
                <li mix={explainCss}>Their history is given to {agentWord(chat.adapter)} for this message only.</li>
              </ul>
            </div>
          ) : null}
          <textarea
            aria-label="Message"
            rows={1}
            placeholder={disabled ? disabledReason || 'This chat cannot take messages.' : running ? `${who} is answering... (Stop to interrupt)` : empty ? `Message ${who}` : `Reply to ${who}`}
            disabled={disabled}
            mix={[
              ref((n) => (area = n as HTMLTextAreaElement)),
              on('input', (e) => {
                text = (e.currentTarget as HTMLTextAreaElement).value;
                fit();
                void handle.update();
              }),
              on('keydown', (e) => {
                const k = e as KeyboardEvent;
                if (sendsOnKey(k)) {
                  k.preventDefault();
                  void submit();
                }
              }),
              on('paste', (e) => {
                const fs = (e as ClipboardEvent).clipboardData?.files;
                if (fs && fs.length && !disabled) {
                  e.preventDefault();
                  void addFiles(fs);
                }
              }),
            ]}
          />
          <div mix={barCss}>
            {controls}
            <input
              type="file"
              multiple
              hidden
              data-testid="chat-file"
              mix={[
                ref((n) => (fileInput = n as HTMLInputElement)),
                on('change', (e) => {
                  const el = e.currentTarget as HTMLInputElement;
                  if (el.files && el.files.length) void addFiles(el.files).then(() => (el.value = ''));
                }),
              ]}
            />
            <button
              type="button"
              mix={[smallBtn, quietBtn, iconBtn, on('click', () => fileInput?.click())]}
              aria-label="Attach files"
              title="Attach files (or drop or paste them here)"
              disabled={disabled || files.length >= MAX_FILES}
            >
              <Icon icon={Paperclip} size={14} />
            </button>
            <Popover
              open={picking}
              onOpen={() => ((picking = true), (filter = ''), (search = ''), clearTimeout(timer), void handle.update())}
              onClose={() => ((picking = false), void handle.update())}
              label="Attach chats"
              placement="top-start"
              testId="attach-picker"
              stopOutsideClick={false}
              trigger={(mix) => (
                <button
                  type="button"
                  mix={[smallBtn, quietBtn, mix, attach.length ? undefined : iconBtn]}
                  disabled={disabled}
                  aria-label={`Attach chats${attach.length ? ` (${attach.length})` : ''}`}
                  aria-expanded={picking ? 'true' : 'false'}
                  title="Give the history of other chats to this message"
                >
                  <Icon icon={MessagesSquare} size={14} />
                  {attach.length ? <span aria-hidden="true">{attach.length}</span> : null}
                </button>
              )}
            >
              <h2>Attach chats</h2>
              <p mix={noteCss}>
                Their history is given to {agentWord(chat.adapter)} for this message only, quoted as data. Up to {MAX_ATTACH}.
              </p>
              <input type="search" aria-label="Filter chats" placeholder="Search all chats" value={filter} mix={on('input', (e) => onFilter(e.currentTarget.value))} />
              {others.loading && !others.data ? (
                <Loading label="Loading chats..." />
              ) : list.length === 0 ? (
                <p mix={noteCss}>{search ? 'Nothing matches.' : 'There are no other chats yet.'}</p>
              ) : (
                <ul mix={pickList} aria-label="Chats to attach">
                  {list.map((c) => {
                    const on_ = attach.includes(c.id);
                    return (
                      <li key={c.id}>
                        <button type="button" aria-pressed={on_ ? 'true' : 'false'} disabled={!on_ && attach.length >= MAX_ATTACH} data-testid="attach-option" mix={on('click', () => pick(c))}>
                          <input type="checkbox" checked={on_} tabIndex={-1} aria-hidden="true" />
                          <span class="t">{chatTitle(c)}</span>
                          <span class="w">{relTime(c.updatedAt)}</span>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              )}
              <ErrorNote error={others.error} />
            </Popover>
            <span style={{ marginLeft: 'auto' }} />
            {running ? (
              <button type="button" mix={[stopCss, on('click', () => onStop())]} disabled={stopping} data-testid="chat-stop">
                <Icon icon={Square} size={12} />
                {stopping ? 'Stopping...' : 'Stop'}
              </button>
            ) : null}
            <button
              type="submit"
              mix={sendCss}
              disabled={disabled || running || sending || !text.trim()}
              aria-label={sending ? 'Sending...' : 'Send'}
              aria-keyshortcuts="Enter"
              title={running ? 'Wait for the answer, or stop it' : 'Send (Enter); Shift+Enter starts a new line'}
            >
              <kbd aria-hidden="true">↵</kbd>
              <span aria-hidden="true">send</span>
            </button>
          </div>
        </div>
      </form>
    );
  };
}
