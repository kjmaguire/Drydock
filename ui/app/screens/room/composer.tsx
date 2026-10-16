// The room composer (brief 21): a rounded raised box at the bottom of the channel with an attach button, the text, a send
// button, the @mention / @role: / #channel / slash command suggestions above it, and the command help. Ctrl+Enter sends.
// What a mention means is decided by the room core when the message is posted; files go with the message.

import { css, navigate, on, ref, type Handle } from 'remix/component';
import { ArrowUp, AtSign, Hash, Paperclip, SquareSlash, Users, X } from 'lucide';
import type { Agent, Room, RoomMember } from '../../api/types.ts';
import { activeToken, applySuggestion, mentionHandle, parseSlash, SLASH_COMMANDS, suggest, type MentionTarget, type Suggestion, type Token } from '../../lib/composer.ts';
import { authorName, roomLabel } from '../../lib/format.ts';
import { routes } from '../../routes.ts';
import { services } from '../../state/services.tsx';
import { IconButton } from '../../ui/button.tsx';
import { Avatar } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Icon } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';
import { checkFiles, formatSize, MAX_FILES } from './messages.ts';

// The textarea keeps its native textbox role (ARIA in HTML allows no role="combobox" on a <textarea>) and drives the
// suggestion list the way a textbox may: aria-haspopup, aria-autocomplete, aria-controls and aria-activedescendant.

interface Pending {
  name: string;
  size: number;
  data: Uint8Array;
}

export const composerWrap = css({ display: 'grid', gap: t.s2, padding: `0 ${t.s5} ${t.s4}`, flex: 'none' });

/** The raised box itself (also used by the thread reply form). */
export const composerBox = css({
  position: 'relative',
  display: 'grid',
  border: `1px solid ${t.borderStrong}`,
  borderRadius: t.rXl,
  background: t.bgRaised,
  boxShadow: t.shadowSm,
  transition: `border-color ${t.fast} ${t.ease}, box-shadow ${t.fast} ${t.ease}`,
  '&:focus-within': { borderColor: t.accent, boxShadow: `0 0 0 3px ${t.accentSoft}` },
  '&[data-drag="true"]': { borderColor: t.accent, borderStyle: 'dashed', background: t.accentSoft },
  '&[data-disabled="true"]': { opacity: '0.7' },
  '& textarea': {
    display: 'block',
    minHeight: '44px',
    maxHeight: '40vh',
    padding: '12px 14px 4px',
    border: '0',
    borderRadius: t.rXl,
    background: 'transparent',
    resize: 'none',
    fontSize: t.textMd,
    lineHeight: '1.5',
    boxShadow: 'none',
  },
  '& textarea:focus-visible': { boxShadow: 'none' },
});

export const composerBar = css({ display: 'flex', alignItems: 'center', gap: t.s1, padding: `4px ${t.s2} ${t.s2} ${t.s2}` });
const hintCss = css({
  flex: '1',
  minWidth: '0',
  color: t.textFaint,
  fontSize: t.textXs,
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  '& kbd': { margin: '0 2px' },
});
// the send button is a round black disc with an arrow (Crew HQ, decision P64); its name stays "Send" for readers
export const sendCss = css({
  display: 'grid',
  placeItems: 'center',
  flex: 'none',
  width: '36px',
  height: '36px',
  minHeight: '0',
  padding: '0',
  border: '0',
  borderRadius: t.rFull,
  background: t.accent,
  color: t.onAccent,
  transition: `background-color ${t.fast} ${t.ease}, transform ${t.fast} ${t.ease}`,
  '&:hover': { background: t.accentHover, transform: 'translateY(-1px)' },
  '&:disabled': { background: t.bgActive, color: t.textFaint, opacity: '1', transform: 'none' },
});

const filesCss = css({ display: 'flex', flexWrap: 'wrap', gap: t.s2, margin: '0', padding: `10px 12px 0`, listStyle: 'none' });
const fileChip = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  maxWidth: '260px',
  height: '28px',
  padding: '0 4px 0 10px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rMd,
  background: t.bgSurface,
  fontSize: t.textSm,
  '& .name': { whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', fontWeight: '500' },
  '& .size': { color: t.textMuted, flex: 'none' },
  '& button': { width: '20px', height: '20px' },
});

const suggestCss = css({
  position: 'absolute',
  left: '0',
  right: '0',
  bottom: 'calc(100% + 6px)',
  zIndex: '5',
  margin: '0',
  padding: '4px',
  maxHeight: '300px',
  overflowY: 'auto',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgOverlay,
  boxShadow: t.shadowMd,
});
const suggestHead = css({ padding: '6px 8px 4px', color: t.textFaint, fontSize: t.textXs, fontWeight: '600', letterSpacing: '0.04em', textTransform: 'uppercase' });
const optionCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  minHeight: '34px',
  padding: '4px 8px',
  borderRadius: t.rMd,
  cursor: 'pointer',
  fontSize: t.textMd,
  '& .detail': { marginLeft: 'auto', color: t.textMuted, fontSize: t.textSm, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& .ico': { display: 'grid', placeItems: 'center', width: '24px', height: '24px', borderRadius: t.rMd, background: t.bgActive, color: t.textMuted, flex: 'none' },
  '&[aria-selected="true"]': { background: t.accentSoft },
  '&:hover': { background: t.bgHover },
  '&[aria-selected="true"]:hover': { background: t.accentSoft },
});

const helpCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s3} ${t.s4}`,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  fontSize: t.textMd,
  '& .top': { display: 'flex', alignItems: 'center', justifyContent: 'space-between' },
  '& ul': { display: 'grid', gap: '4px', margin: '0', padding: '0', listStyle: 'none' },
  '& li': { display: 'flex', gap: t.s3, alignItems: 'baseline' },
  '& li code': { flex: 'none', minWidth: '170px' },
  '& p': { color: t.textMuted, fontSize: t.textSm, margin: '0' },
});
const noteCss = css({ margin: '0', color: t.textMuted, fontSize: t.textSm });

function suggestionGroup(s: Suggestion | undefined): string {
  if (!s) return '';
  if (s.insert.startsWith('/')) return 'Commands';
  if (s.insert.startsWith('#')) return 'Channels';
  return 'Members';
}

export interface ComposerProps {
  room: Room;
  agents: readonly Agent[];
  members: readonly RoomMember[];
  rooms: readonly Room[];
  disabled: boolean;
  threadRoot?: string;
  onSent: () => void;
  /** Where /task goes after creating the task (defaults to the router's navigate). */
  onNavigate?: (href: string) => void;
}

export function Composer(handle: Handle<ComposerProps>) {
  const s = services(handle);
  let text = '';
  let caret = 0;
  let files: Pending[] = [];
  let busy = false;
  let error: Error | undefined;
  let note = '';
  let help = false;
  let active = 0;
  let dismissed: number | undefined;
  let drag = false;
  let area: HTMLTextAreaElement | undefined;
  let fileInput: HTMLInputElement | undefined;
  const id = `composer-input-${handle.id}`;
  const listId = `composer-suggest-${handle.id}`;

  const update = () => void handle.update();

  const fit = () => {
    if (!area) return;
    area.style.height = 'auto';
    area.style.height = `${Math.min(area.scrollHeight, Math.round(window.innerHeight * 0.4))}px`;
  };

  const setText = (v: string, c: number = v.length) => {
    text = v;
    caret = c;
    if (area && area.value !== v) area.value = v;
    if (area && document.activeElement === area) area.setSelectionRange(c, c);
    fit();
  };

  const targets = (): MentionTarget[] => {
    const { members, agents } = handle.props;
    return members.map((m) => ({ kind: m.member.kind, id: m.member.id, name: authorName(m.member, agents), role: m.role }));
  };

  const current = (): { tok: Token | undefined; list: Suggestion[] } => {
    if (handle.props.disabled) return { tok: undefined, list: [] };
    const tok = activeToken(text, caret);
    if (!tok || dismissed === tok.start) return { tok, list: [] };
    const rooms = handle.props.rooms.filter((r) => !r.archived && (r.kind === 'channel' || r.kind === 'task'));
    return { tok, list: suggest(tok, { members: targets(), rooms }) };
  };

  const pick = (tok: Token, sg: Suggestion) => {
    const r = applySuggestion(text, tok, sg);
    active = 0;
    setText(r.text, r.caret);
    area?.focus();
    area?.setSelectionRange(r.caret, r.caret);
    update();
  };

  const addFiles = async (list: FileList | File[]) => {
    error = undefined;
    const incoming = Array.from(list);
    const bad = checkFiles(files.length, incoming);
    if (bad) {
      error = new Error(bad);
      update();
      return;
    }
    const read: Pending[] = [];
    for (const f of incoming) read.push({ name: f.name, size: f.size, data: new Uint8Array(await f.arrayBuffer()) });
    files = [...files, ...read];
    update();
  };

  const reset = () => {
    setText('');
    files = [];
    handle.props.onSent();
  };

  const send = async () => {
    const { room, agents, disabled, threadRoot } = handle.props;
    if (busy || disabled) return;
    const p = parseSlash(text);
    if (p.kind === 'post' && !p.body && files.length === 0) return;
    if (p.kind !== 'post' && files.length > 0) {
      error = new Error('Files go with a message, not with a command.');
      update();
      return;
    }
    busy = true;
    error = undefined;
    note = '';
    update();
    try {
      switch (p.kind) {
        case 'post':
          if (files.length) await s.client.roomAttach(room.id, p.body, files, threadRoot);
          else await s.client.roomPost(room.id, p.body, threadRoot);
          reset();
          break;
        case 'task': {
          const created = await s.client.taskCreate({ projectId: room.projectId, title: p.title });
          reset();
          const href = routes.task.href({ id: created.id });
          if (handle.props.onNavigate) handle.props.onNavigate(href);
          else void navigate(href);
          break;
        }
        case 'invite': {
          const names = agents.map((a) => a.name);
          const a = agents.find((x) => x.id === p.handle || mentionHandle(x.name, x.id, names) === p.handle.toLowerCase());
          if (!a) throw new Error(`No agent of this project is called @${p.handle}.`);
          await s.client.roomInvite(room.id, a.id);
          note = `${a.name} was added to the room.`;
          reset();
          break;
        }
        case 'mode':
          await s.client.roomSetMode({ roomId: room.id, mode: p.mode });
          note = `The room is now in ${p.mode} mode.`;
          reset();
          break;
        case 'help':
          help = true;
          setText('');
          break;
        case 'error':
          throw new Error(p.message);
      }
    } catch (err) {
      error = err instanceof Error ? err : new Error(String(err));
    } finally {
      busy = false;
      update();
    }
  };

  const onKey = (e: KeyboardEvent) => {
    const { tok, list } = current();
    if (tok && list.length > 0) {
      const idx = Math.min(active, list.length - 1);
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        active = (idx + 1) % list.length;
        update();
        return;
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault();
        active = (idx - 1 + list.length) % list.length;
        update();
        return;
      }
      if ((e.key === 'Enter' && !e.ctrlKey && !e.metaKey) || e.key === 'Tab') {
        e.preventDefault();
        pick(tok, list[idx]!);
        return;
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        dismissed = tok.start;
        update();
        return;
      }
    }
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      void send();
    }
  };

  return () => {
    const { room, agents, disabled } = handle.props;
    const { tok, list } = current();
    const open = list.length > 0;
    const idx = Math.min(active, Math.max(list.length - 1, 0));
    const label = roomLabel(room, agents);
    let lastGroup = '';
    const people = open ? targets() : [];
    return (
      <form
        mix={[
          composerWrap,
          on('submit', (e) => {
            e.preventDefault();
            void send();
          }),
          on('dragover', (e) => {
            e.preventDefault();
            if (!disabled && !drag) {
              drag = true;
              update();
            }
          }),
          on('dragleave', (e) => {
            if (e.currentTarget.contains(e.relatedTarget as Node | null)) return;
            drag = false;
            update();
          }),
          on('drop', (e) => {
            drag = false;
            update();
            if (!e.dataTransfer?.files?.length || disabled) return;
            e.preventDefault();
            void addFiles(e.dataTransfer.files);
          }),
        ]}
        aria-label="Message composer"
        class="composer"
      >
        <ErrorNote error={error} what="Not sent" />
        {note ? (
          <p mix={noteCss} role="status">
            {note}
          </p>
        ) : null}
        {help ? (
          <div mix={helpCss} role="region" aria-label="Commands">
            <div class="top">
              <strong>Commands</strong>
              <IconButton
                icon={X}
                label="Close"
                size={14}
                mix={[
                  on('click', () => {
                    help = false;
                    update();
                  }),
                ]}
              />
            </div>
            <ul>
              {SLASH_COMMANDS.map((c) => (
                <li key={c.name}>
                  <code>
                    /{c.name}
                    {c.args ? ` ${c.args}` : ''}
                  </code>
                  <span>{c.help}</span>
                </li>
              ))}
            </ul>
            <p>Type @ to mention someone in the room, @role: for everyone with a role, # for a channel. Markers: [DECISION] [TODO] [STATUS] [RESULT] at the start of a line.</p>
          </div>
        ) : null}
        <div mix={composerBox} data-drag={drag ? 'true' : undefined} data-disabled={disabled ? 'true' : undefined}>
          {open && tok ? (
            <ul mix={suggestCss} role="listbox" id={listId} aria-label="Suggestions">
              {list.map((sg, i) => {
                const g = suggestionGroup(sg);
                const headRow = g !== lastGroup ? (lastGroup = g) : '';
                return [
                  headRow ? (
                    <li key={`h-${g}`} role="presentation" mix={suggestHead}>
                      {g}
                    </li>
                  ) : null,
                  <li
                    key={sg.insert + i}
                    id={`${listId}-${i}`}
                    role="option"
                    aria-selected={i === idx ? 'true' : 'false'}
                    mix={[
                      optionCss,
                      on('mousedown', (e) => {
                        e.preventDefault();
                        pick(tok, sg);
                      }),
                    ]}
                  >
                    {sg.insert.startsWith('@role:') ? (
                      <span class="ico">
                        <Icon icon={Users} size={14} />
                      </span>
                    ) : sg.insert.startsWith('@') ? (
                      <Avatar id={people.find((x) => x.name === sg.label)?.id ?? sg.insert} name={sg.label} size={24} kind={people.find((x) => x.name === sg.label)?.kind === 'human' ? 'human' : undefined} />
                    ) : sg.insert.startsWith('#') ? (
                      <span class="ico">
                        <Icon icon={Hash} size={14} />
                      </span>
                    ) : (
                      <span class="ico">
                        <Icon icon={SquareSlash} size={14} />
                      </span>
                    )}
                    <strong>{sg.label}</strong>
                    {sg.detail ? <span class="detail">{sg.detail}</span> : null}
                  </li>,
                ];
              })}
            </ul>
          ) : null}
          {files.length ? (
            <ul mix={filesCss} aria-label="Files to attach">
              {files.map((f, i) => (
                <li key={`${f.name}-${i}`} mix={fileChip}>
                  <Icon icon={Paperclip} size={13} />
                  <span class="name">{f.name}</span> <span class="size">{formatSize(f.size)}</span>
                  <IconButton
                    icon={X}
                    size={12}
                    label={`Remove ${f.name}`}
                    mix={[
                      on('click', () => {
                        files = files.filter((_, j) => j !== i);
                        update();
                      }),
                    ]}
                  />
                </li>
              ))}
            </ul>
          ) : null}
          <label for={id} class="sr-only">
            Message to #{room.name}
          </label>
          <textarea
            id={id}
            rows={1}
            placeholder={disabled ? 'This channel is archived' : `Message ${room.kind === 'dm' ? '' : '#'}${label}`}
            disabled={disabled}
            aria-haspopup="listbox"
            aria-controls={open ? listId : undefined}
            aria-activedescendant={open ? `${listId}-${idx}` : undefined}
            aria-autocomplete="list"
            mix={[
              ref((node) => {
                area = node as HTMLTextAreaElement;
                if (area.value !== text) area.value = text;
              }),
              on('input', (e) => {
                const el = e.currentTarget as HTMLTextAreaElement;
                text = el.value;
                caret = el.selectionStart ?? el.value.length;
                active = 0;
                dismissed = undefined;
                fit();
                update();
              }),
              on('select', (e) => {
                const c = (e.currentTarget as HTMLTextAreaElement).selectionStart ?? 0;
                if (c !== caret) {
                  caret = c;
                  update();
                }
              }),
              on('click', (e) => {
                const c = (e.currentTarget as HTMLTextAreaElement).selectionStart ?? 0;
                if (c !== caret) {
                  caret = c;
                  update();
                }
              }),
              on('keydown', (e) => onKey(e as KeyboardEvent)),
              on('paste', (e) => {
                const fs = (e as ClipboardEvent).clipboardData?.files;
                if (fs && fs.length && !disabled) {
                  e.preventDefault();
                  void addFiles(fs);
                }
              }),
            ]}
          />
          <div mix={composerBar}>
            <input
              type="file"
              multiple
              hidden
              data-testid="composer-file"
              mix={[
                ref((node) => {
                  fileInput = node as HTMLInputElement;
                }),
                on('change', (e) => {
                  const el = e.currentTarget as HTMLInputElement;
                  if (el.files && el.files.length) void addFiles(el.files).then(() => (el.value = ''));
                }),
              ]}
            />
            <IconButton
              icon={Paperclip}
              label="Attach"
              title="Attach files (or drop or paste them here)"
              disabled={disabled || files.length >= MAX_FILES}
              mix={[on('click', () => fileInput?.click())]}
            />
            <IconButton
              icon={SquareSlash}
              label="Show commands"
              aria-pressed={help ? 'true' : 'false'}
              disabled={disabled}
              mix={[
                on('click', () => {
                  help = !help;
                  update();
                }),
              ]}
            />
            <IconButton
              icon={AtSign}
              label="Mention someone"
              disabled={disabled}
              mix={[
                on('click', () => {
                  const before = text.slice(0, caret);
                  const pad = before && !/\s$/.test(before) ? ' ' : '';
                  const next = `${before}${pad}@${text.slice(caret)}`;
                  dismissed = undefined;
                  area?.focus();
                  setText(next, before.length + pad.length + 1);
                  update();
                }),
              ]}
            />
            <span mix={hintCss} aria-hidden="true">
              {disabled ? '' : (
                <>
                  <kbd>Ctrl</kbd>+<kbd>Enter</kbd> to send · <kbd>/</kbd> for commands · <kbd>@</kbd> to mention
                </>
              )}
            </span>
            <button type="submit" mix={sendCss} disabled={disabled || busy || (!text.trim() && files.length === 0)} title={busy ? 'Sending...' : 'Send (Ctrl+Enter)'}>
              <Icon icon={ArrowUp} size={18} />
              <span class="sr-only">{busy ? 'Sending...' : 'Send'}</span>
            </button>
          </div>
        </div>
      </form>
    );
  };
}
