import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import type { Client } from '../api/client';
import type { Agent, Message, Room, RoomMember } from '../api/types';
import { activeToken, applySuggestion, mentionHandle, parseSlash, suggest } from '../lib/composer';
import { AppServicesProvider } from '../state/context';
import { UiProvider } from '../state/ui';
import { services } from '../test/fakes';
import { Attachments } from './Attachments';
import { CommandPalette } from './CommandPalette';
import { Composer } from './Composer';

beforeAll(() => {
  // cmdk measures and scrolls its list; jsdom has neither
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  Element.prototype.scrollIntoView ??= function () {};
});

const withServices = (ui: React.ReactElement, client: Partial<Client>) =>
  render(
    <AppServicesProvider services={services(client)}>
      <UiProvider onReveal={() => {}}>{ui}</UiProvider>
    </AppServicesProvider>,
  );

const room: Room = { id: 'room_1', projectId: 'prj_1', kind: 'channel', name: 'general', mode: 'auto', archived: false, createdAt: '', version: 1 };
const agents = [
  { id: 'agt_1', name: 'Backend One', role: 'backend' },
  { id: 'agt_2', name: 'Reviewer #1', role: 'reviewer' },
] as Agent[];
const members: RoomMember[] = [
  { roomId: 'room_1', member: { kind: 'human', id: 'kyle', display: 'Kyle' }, state: 'active', addedAt: '', lastReadSeq: 0 },
  { roomId: 'room_1', member: { kind: 'agent', id: 'agt_1' }, role: 'backend', state: 'active', addedAt: '', lastReadSeq: 0 },
  { roomId: 'room_1', member: { kind: 'agent', id: 'agt_2' }, role: 'reviewer', state: 'active', addedAt: '', lastReadSeq: 0 },
  { roomId: 'room_1', member: { kind: 'tool', id: 'tests' }, state: 'active', addedAt: '', lastReadSeq: 0 },
];

describe('composer typing aids', () => {
  it('finds the token at the caret', () => {
    expect(activeToken('hi @ba', 6)).toEqual({ sigil: '@', query: 'ba', start: 3, end: 6 });
    expect(activeToken('ping @role:rev', 14)).toMatchObject({ sigil: '@role:', query: 'rev' });
    expect(activeToken('see #gen', 8)).toMatchObject({ sigil: '#', query: 'gen' });
    expect(activeToken('/ta', 3)).toMatchObject({ sigil: '/', query: 'ta' });
    expect(activeToken('a /ta', 5)).toBeUndefined();
    expect(activeToken('mail a@b', 8)).toBeUndefined();
    expect(activeToken('hello', 5)).toBeUndefined();
  });

  it('inserts a handle the room resolves: the name when it is a clean unique handle, else the id', () => {
    expect(mentionHandle('Backend One', 'agt_1', ['Backend One', 'Reviewer #1'])).toBe('backend-one');
    expect(mentionHandle('Reviewer #1', 'agt_2', ['Backend One', 'Reviewer #1'])).toBe('agt_2');
    expect(mentionHandle('Sam', 'agt_3', ['Sam', 'sam'])).toBe('agt_3');
  });

  it('suggests members, roles, rooms and commands, best match first', () => {
    const ctx = { members: [{ kind: 'agent' as const, id: 'agt_1', name: 'Backend One', role: 'backend' }, { kind: 'tool' as const, id: 'tests', name: 'Tests' }], rooms: [{ name: 'general', kind: 'channel' }, { name: 'task-3-subtract', kind: 'task' }] };
    expect(suggest({ sigil: '@', query: 'back', start: 0, end: 5 }, ctx)[0]).toMatchObject({ label: 'Backend One', insert: '@backend-one ' });
    expect(suggest({ sigil: '@', query: 'tes', start: 0, end: 4 }, ctx)[0]?.insert).toBe('@tests ');
    expect(suggest({ sigil: '@role:', query: 'b', start: 0, end: 7 }, ctx)[0]?.insert).toBe('@role:backend ');
    expect(suggest({ sigil: '#', query: 'task', start: 0, end: 5 }, ctx).map((s) => s.insert)).toEqual(['#task-3-subtract ']);
    expect(suggest({ sigil: '/', query: 'de', start: 0, end: 3 }, ctx)[0]?.insert).toBe('/decision ');
    const r = applySuggestion('hi @ba there', { sigil: '@', query: 'ba', start: 3, end: 6 }, { label: '', insert: '@backend-one ' });
    expect(r).toEqual({ text: 'hi @backend-one there', caret: 16 });
  });

  it('parses slash commands into actions, and leaves plain text alone', () => {
    expect(parseSlash('hello')).toEqual({ kind: 'post', body: 'hello' });
    expect(parseSlash('/decision Use SQLite')).toEqual({ kind: 'post', body: '[DECISION] Use SQLite' });
    expect(parseSlash('/task Add Multiply')).toEqual({ kind: 'task', title: 'Add Multiply' });
    expect(parseSlash('/invite @backend-one')).toEqual({ kind: 'invite', handle: 'backend-one' });
    expect(parseSlash('/mode sequential')).toEqual({ kind: 'mode', mode: 'sequential' });
    expect(parseSlash('/help')).toEqual({ kind: 'help' });
    expect(parseSlash('/task').kind).toBe('error');
    expect(parseSlash('/mode moderator').kind).toBe('error');
    expect(parseSlash('/rm -rf').kind).toBe('error');
  });
});

describe('the composer', () => {
  const setup = (client: Partial<Client>) =>
    withServices(<Composer room={room} agents={agents} members={members} rooms={[room, { ...room, id: 'room_2', name: 'task-3-subtract', kind: 'task' }]} disabled={false} onSent={() => {}} />, client);

  it('offers mentions as you type and inserts the chosen one with the keyboard', async () => {
    const roomPost = vi.fn(async () => ({}));
    setup({ roomPost });
    const box = screen.getByLabelText('Message to #general') as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: 'ask @rev', selectionStart: 8 } });
    expect(screen.getByRole('listbox', { name: 'Suggestions' })).toBeTruthy();
    expect(screen.getAllByRole('option')[0]!.textContent).toContain('Reviewer #1');
    fireEvent.keyDown(box, { key: 'Enter' });
    expect(box.value).toBe('ask @agt_2 ');
    expect(screen.queryByRole('listbox')).toBeNull();
    fireEvent.change(box, { target: { value: 'ask @agt_2 to look', selectionStart: 18 } });
    fireEvent.keyDown(box, { key: 'Enter', ctrlKey: true });
    await waitFor(() => expect(roomPost).toHaveBeenCalledWith('room_1', 'ask @agt_2 to look', undefined));
  });

  it('runs slash commands: markers, task creation, invite, help; unknown commands are refused', async () => {
    const roomPost = vi.fn(async () => ({}));
    const taskCreate = vi.fn(async () => ({ id: 'task_9' }) as never);
    const roomInvite = vi.fn(async () => []);
    setup({ roomPost, taskCreate, roomInvite });
    const box = screen.getByLabelText('Message to #general') as HTMLTextAreaElement;
    const send = async (v: string) => {
      fireEvent.change(box, { target: { value: v, selectionStart: v.length } });
      fireEvent.keyDown(box, { key: 'Escape' });
      fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    };
    await send('/decision Keep SQLite');
    await waitFor(() => expect(roomPost).toHaveBeenCalledWith('room_1', '[DECISION] Keep SQLite', undefined));
    await send('/invite @backend-one');
    await waitFor(() => expect(roomInvite).toHaveBeenCalledWith('room_1', 'agt_1'));
    expect(await screen.findByText('Backend One was added to the room.')).toBeTruthy();
    await send('/help');
    expect(await screen.findByRole('region', { name: 'Commands' })).toBeTruthy();
    await send('/rm everything');
    expect(await screen.findByText(/is not a command/)).toBeTruthy();
    await send('/task Add Multiply');
    await waitFor(() => expect(taskCreate).toHaveBeenCalledWith({ projectId: 'prj_1', title: 'Add Multiply' }));
    await waitFor(() => expect(window.location.hash).toBe('#/task/task_9'));
  });

  it('attaches files to the message and sends them together', async () => {
    const roomAttach = vi.fn(async () => ({}));
    setup({ roomAttach, roomPost: vi.fn() });
    const input = screen.getByTestId('composer-file') as HTMLInputElement;
    const file = new File(['hello'], 'notes.txt', { type: 'text/plain' });
    await act(async () => {
      fireEvent.change(input, { target: { files: [file] } });
    });
    expect(await screen.findByText('notes.txt')).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Message to #general'), { target: { value: 'see notes', selectionStart: 9 } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));
    await waitFor(() => expect(roomAttach).toHaveBeenCalled());
    const [rid, body, files] = roomAttach.mock.calls[0] as unknown as [string, string, { name: string; data: Uint8Array }[]];
    expect([rid, body, files[0]!.name, new TextDecoder().decode(files[0]!.data)]).toEqual(['room_1', 'see notes', 'notes.txt', 'hello']);
  });
});

describe('attachments on a message', () => {
  it('lists the files and saves one through the core', async () => {
    const roomAttachment = vi.fn(async () => ({ id: 'att_1', name: 'notes.txt', mime: 'text/plain', size: 5, data: new TextEncoder().encode('hello') }));
    globalThis.URL.createObjectURL ??= () => 'blob:x';
    globalThis.URL.revokeObjectURL ??= () => {};
    const m = { id: 'msg_1', payload: { attachments: [{ id: 'att_1', name: 'notes.txt', mime: 'text/plain', size: 5 }, { id: 'att_2', name: 'logo.png', mime: 'image/png', size: 2048 }] } } as unknown as Message;
    withServices(<Attachments m={m} />, { roomAttachment });
    expect(screen.getAllByTestId('attachment')).toHaveLength(2);
    expect(screen.getAllByRole('button', { name: 'Show image' })).toHaveLength(1);
    fireEvent.click(screen.getAllByRole('button', { name: 'Save' })[0]!);
    expect(await screen.findByRole('link', { name: 'Download notes.txt' })).toBeTruthy();
    expect(roomAttachment).toHaveBeenCalledWith('att_1');
  });
});

describe('the command palette', () => {
  it('opens with Ctrl+K, filters, and goes where you pick', async () => {
    window.location.hash = '#/';
    withServices(<CommandPalette rooms={[room]} tasks={[{ id: 'task_3', seq: 3, title: 'Subtract', status: 'REVIEW', runId: 'run_1' } as never]} agents={agents} onTheme={() => {}} onMessage={() => {}} />, {});
    expect(screen.queryByRole('dialog')).toBeNull();
    act(() => {
      fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    });
    const input = await screen.findByLabelText('Command');
    fireEvent.change(input, { target: { value: 'workflow of #3' } });
    const item = await screen.findByText('Workflow of #3 Subtract');
    fireEvent.click(item);
    expect(window.location.hash).toBe('#/task/task_3/run');
    expect(screen.queryByLabelText('Command')).toBeNull();
  });
});
