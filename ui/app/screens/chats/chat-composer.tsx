// The chat composer: a textarea (Ctrl+Enter sends, like the room), "Attach chats" (a picker of other chats; their history
// is given to the agent for this one message) with the attached chats as removable chips, and Send, or Stop while a turn
// runs. A send while a turn runs is not possible (the core refuses it too: one turn at a time).

import { css, on, ref, type Handle } from 'remix/component';
import { ArrowUp, MessagesSquare, Paperclip, Square, X } from 'lucide';
import type { Chat, ChatListItem } from '../../api/chatClient.ts';
import { query, services } from '../../state/services.tsx';
import { Icon } from '../../ui/icon.tsx';
import { ErrorNote, Loading } from '../../ui/feedback.tsx';
import { Popover } from '../../ui/popover.tsx';
import { t } from '../../ui/tokens.ts';
import { composerBar, composerBox, composerWrap, sendCss } from '../room/composer.tsx';
import { adapterLabel, agentWord, chatTitle, MAX_ATTACH, relTime } from './chats.ts';

const hintCss = css({ flex: '1', minWidth: '0', color: t.textFaint, fontSize: t.textXs, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', '& kbd': { margin: '0 2px' } });
const attachBtn = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '5px',
  height: '28px',
  minHeight: '0',
  padding: '0 8px',
  border: '0',
  borderRadius: t.rMd,
  background: 'transparent',
  color: t.textMuted,
  fontSize: t.textSm,
  fontWeight: '500',
  '&:hover': { background: t.bgHover, color: t.text },
  '&[aria-expanded="true"]': { background: t.bgActive, color: t.text },
  '&:disabled': { opacity: '0.5', pointerEvents: 'none' },
});
const stopCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  height: '30px',
  minHeight: '0',
  padding: '0 12px',
  border: `1px solid ${t.borderStrong}`,
  borderRadius: t.rLg,
  background: t.bgRaised,
  color: t.text,
  fontWeight: '600',
  '&:hover': { background: t.dangerSoft, color: t.danger, borderColor: 'transparent' },
});
const chipsCss = css({ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '6px', margin: '0', padding: '10px 12px 0', listStyle: 'none' });
const chipCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '5px',
  maxWidth: '260px',
  height: '24px',
  padding: '0 2px 0 8px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  fontSize: t.textSm,
  '& .name': { whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', fontWeight: '500' },
  '& button': { display: 'grid', placeItems: 'center', width: '20px', height: '20px', minHeight: '0', padding: '0', border: '0', borderRadius: t.rSm, background: 'transparent', color: t.textMuted },
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
    minHeight: '0',
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

export function ChatComposer(
  handle: Handle<{
    chat: Chat;
    running: boolean;
    disabled: boolean;
    disabledReason: string;
    sending: boolean;
    stopping: boolean;
    onSend: (text: string, attach: string[]) => Promise<boolean>;
    onStop: () => void;
  }>,
) {
  const s = services(handle);
  let text = '';
  let attach: string[] = [];
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

  const canSend = () => !handle.props.disabled && !handle.props.running && !handle.props.sending && text.trim().length > 0;

  const submit = async () => {
    if (!canSend()) return;
    const ok = await handle.props.onSend(text, attach);
    if (ok) {
      text = '';
      attach = [];
      if (area) {
        area.value = '';
        area.style.height = '';
      }
      void handle.update();
    }
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
    const { chat, running, disabled, disabledReason, sending, stopping, onStop } = handle.props;
    const list: ChatListItem[] = (others.data ?? []).filter((c) => c.id !== chat.id);
    const titleOf = (id: string) => {
      const c = list.find((x) => x.id === id);
      return titles.get(id) ?? (c ? chatTitle(c) : id);
    };
    const who = adapterLabel(chat.adapter);
    return (
      <form
        mix={[
          composerWrap,
          on('submit', (e) => {
            e.preventDefault();
            void submit();
          }),
        ]}
        aria-label="Send a message"
        data-testid="chat-composer"
      >
        <div mix={composerBox} data-disabled={disabled ? 'true' : undefined}>
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
            placeholder={disabled ? disabledReason || 'This chat cannot take messages.' : running ? `${who} is answering... (Stop to interrupt)` : `Message ${who}`}
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
                if (k.key === 'Enter' && (k.ctrlKey || k.metaKey)) {
                  k.preventDefault();
                  void submit();
                }
              }),
            ]}
          />
          <div mix={composerBar}>
            <Popover
              open={picking}
              onOpen={() => ((picking = true), (filter = ''), (search = ''), clearTimeout(timer), void handle.update())}
              onClose={() => ((picking = false), void handle.update())}
              label="Attach chats"
              placement="top-start"
              testId="attach-picker"
              stopOutsideClick={false}
              trigger={(mix) => (
                <button type="button" mix={[attachBtn, mix]} disabled={disabled} aria-expanded={picking ? 'true' : 'false'} title="Give the history of other chats to this message">
                  <Icon icon={Paperclip} size={14} />
                  Attach chats{attach.length ? ` (${attach.length})` : ''}
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
            <span mix={hintCss} aria-hidden="true">
              {disabled ? '' : (
                <>
                  <kbd>Ctrl</kbd>+<kbd>Enter</kbd> to send
                </>
              )}
            </span>
            {running ? (
              <button type="button" mix={[stopCss, on('click', () => onStop())]} disabled={stopping} data-testid="chat-stop">
                <Icon icon={Square} size={12} />
                {stopping ? 'Stopping...' : 'Stop'}
              </button>
            ) : null}
            <button type="submit" mix={sendCss} disabled={disabled || running || sending || !text.trim()} title={running ? 'Wait for the answer, or stop it' : sending ? 'Sending...' : 'Send (Ctrl+Enter)'}>
              <Icon icon={ArrowUp} size={18} />
              <span class="sr-only">{sending ? 'Sending...' : 'Send'}</span>
            </button>
          </div>
        </div>
      </form>
    );
  };
}

