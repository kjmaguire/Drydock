import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Message } from '../../api/types.ts';
import { EventStore } from '../../state/eventStore.ts';
import { FakeTransport } from '../../test/fakes.ts';
import { MessageFeed } from './feed.ts';
import {
  buildRows,
  checkFiles,
  continues,
  dayLabel,
  exportFileName,
  formatSize,
  lastHumanMessageId,
  markersOf,
  memberKey,
  mentionsHuman,
  replyCounts,
  repliesText,
} from './messages.ts';

const msg = (over: Partial<Message>): Message => ({
  id: `m${over.seq ?? 1}`,
  room_id: 'r',
  seq: 1,
  type: 'CHAT',
  author: { kind: 'human', id: 'kyle' },
  body: 'hi',
  trust: 'trusted',
  created_at: '2026-10-09T10:00:00',
  ...over,
});

describe('message grouping', () => {
  it('groups consecutive chat from the same author within five minutes', () => {
    const a = msg({ seq: 1, created_at: '2026-10-09T10:00:00' });
    expect(continues(a, msg({ seq: 2, created_at: '2026-10-09T10:04:00' }))).toBe(true);
    expect(continues(a, msg({ seq: 2, created_at: '2026-10-09T10:06:00' }))).toBe(false);
    expect(continues(a, msg({ seq: 2, author: { kind: 'agent', id: 'a1' } }))).toBe(false);
    expect(continues(a, msg({ seq: 2, type: 'ERROR' }))).toBe(false);
    expect(continues(msg({ seq: 1, type: 'SYSTEM' }), msg({ seq: 2 }))).toBe(false);
    expect(continues(a, msg({ seq: 2, thread_root_id: 'm1' }))).toBe(false);
    expect(continues(undefined, a)).toBe(false);
  });

  it('builds rows with day dividers, group heads and a new-messages line', () => {
    const now = new Date('2026-10-09T12:00:00');
    const rows = buildRows(
      [
        msg({ seq: 1, created_at: '2026-10-08T09:00:00' }),
        msg({ seq: 2, created_at: '2026-10-09T09:00:00' }),
        msg({ seq: 3, created_at: '2026-10-09T09:01:00' }),
        msg({ seq: 4, created_at: '2026-10-09T09:02:00' }),
      ],
      { newAfterSeq: 3, now },
    );
    expect(rows.map((r) => (r.kind === 'message' ? `${r.m.seq}${r.head ? 'h' : 'c'}` : r.kind === 'day' ? r.label : 'new'))).toEqual([
      'Yesterday',
      '1h',
      'Today',
      '2h',
      '3c',
      'new',
      '4h',
    ]);
    expect(buildRows([msg({ seq: 1 })]).some((r) => r.kind === 'new')).toBe(false);
  });

  it('labels days in words', () => {
    const now = new Date('2026-10-09T12:00:00');
    expect(dayLabel('2026-10-09T01:00:00', now)).toBe('Today');
    expect(dayLabel('2026-10-08T23:00:00', now)).toBe('Yesterday');
    expect(dayLabel('2026-10-01T10:00:00', now)).toContain('1');
    expect(dayLabel('', now)).toBe('');
  });
});

describe('message details', () => {
  it('counts thread replies and finds the last human message', () => {
    const list = [msg({ seq: 1 }), msg({ seq: 2, thread_root_id: 'm1' }), msg({ seq: 3, thread_root_id: 'm1', author: { kind: 'agent', id: 'a' } }), msg({ seq: 4, author: { kind: 'agent', id: 'a' } })];
    expect(replyCounts(list).get('m1')).toBe(2);
    expect(lastHumanMessageId(list)).toBe('m2');
    expect(lastHumanMessageId([])).toBeUndefined();
    expect(repliesText(1)).toBe('1 reply');
    expect(repliesText(3)).toBe('3 replies');
  });

  it('keeps only the known markers', () => {
    expect(markersOf(msg({ markers: [{ marker: 'DECISION', line: 1 }, { marker: 'NOPE', line: 2 }] })).map((m) => m.marker)).toEqual(['DECISION']);
  });

  it('highlights a message that mentions a human, directly or through a role', () => {
    const agent = { kind: 'agent' as const, id: 'a1' };
    expect(mentionsHuman(msg({ author: agent, mentions: [{ kind: 'human', id: 'kyle', span: [0, 5] }] }))).toBe(true);
    expect(mentionsHuman(msg({ author: agent, mentions: [{ kind: 'role', id: 'owner', members: ['human:kyle'] }] }))).toBe(true);
    expect(mentionsHuman(msg({ author: agent, mentions: [{ kind: 'agent', id: 'a2' }] }))).toBe(false);
    expect(mentionsHuman(msg({ mentions: [{ kind: 'human', id: 'kyle' }] }))).toBe(false); // your own message
  });

  it('member keys, sizes, export names and file checks', () => {
    const mk = (kind: 'human' | 'agent' | 'tool' | 'system', id: string) => memberKey({ roomId: 'r', member: { kind, id }, state: 'active', addedAt: '', lastReadSeq: 0 });
    expect([mk('human', 'kyle'), mk('agent', 'a1'), mk('tool', 'tests'), mk('system', 'system')]).toEqual(['human:kyle', 'a1', 'tool:tests', 'system']);
    expect([formatSize(5), formatSize(2048), formatSize(3 << 20)]).toEqual(['5 B', '2 KB', '3.0 MB']);
    expect(exportFileName('general', 'minutes', 'markdown')).toBe('general-minutes.md');
    expect(exportFileName('general', 'adr_candidates', 'json')).toBe('general-adr-candidates.json');
    expect(checkFiles(3, [{ name: 'a', size: 1 }, { name: 'b', size: 1 }])).toBe('At most 4 files go with one message.');
    expect(checkFiles(0, [{ name: 'big.bin', size: 11 << 20 }])).toBe('big.bin is over 10 MB.');
    expect(checkFiles(0, [{ name: 'ok', size: 10 }])).toBeUndefined();
  });
});

describe('the message feed', () => {
  const setup = (pages: (after: number, limit: number) => Message[]) => {
    const t = new FakeTransport();
    const store = new EventStore(t, { backoff: () => 10_000 });
    const calls: number[] = [];
    const client = {
      roomMessages: async (_r: string, after = 0, limit = 200) => {
        calls.push(after);
        return pages(after, limit);
      },
    };
    return { t, store, calls, client };
  };
  const tick = () => new Promise((r) => setTimeout(r, 0));
  const all = Array.from({ length: 5 }, (_, i) => msg({ seq: i + 1 }));

  it('reads every page from the last seq, in seq order', async () => {
    const { store, calls, client } = setup((after, limit) => all.filter((m) => m.seq > after).slice(0, limit));
    const feed = new MessageFeed(client, store, 'r', 2).start();
    await tick();
    await tick();
    expect(feed.messages.map((m) => m.seq)).toEqual([1, 2, 3, 4, 5]);
    expect(calls).toEqual([0, 2, 4]);
    expect(feed.loading).toBe(false);
    feed.stop();
  });

  it('extends on room.message_posted for this room only, and re-pulls after a reconnect', async () => {
    let have = all.slice(0, 2);
    const { t, store, calls, client } = setup((after) => have.filter((m) => m.seq > after));
    store.start();
    const feed = new MessageFeed(client, store, 'r').start();
    await tick();
    expect(feed.messages).toHaveLength(2);
    have = all.slice(0, 3);
    store.ingest({ seq: 1, time: '', type: 'room.message_posted', data: { room: 'other' } });
    await tick();
    expect(calls).toEqual([0]);
    store.ingest({ seq: 2, time: '', type: 'room.message_posted', data: { room: 'r' } });
    await tick();
    expect(feed.messages.map((m) => m.seq)).toEqual([1, 2, 3]);
    expect(calls).toEqual([0, 2]);
    have = all;
    t.current.h.onError(new Error('down'));
    await tick();
    expect(calls).toEqual([0, 2, 3]);
    expect(feed.messages).toHaveLength(5);
    feed.stop();
    store.stop();
  });

  it('keeps the error and the messages it had', async () => {
    let fail = false;
    const { store, client } = setup(() => {
      if (fail) throw new Error('core is down');
      return all.slice(0, 1);
    });
    const feed = new MessageFeed(client, store, 'r').start();
    await tick();
    fail = true;
    await feed.pull();
    expect(feed.error?.message).toBe('core is down');
    expect(feed.messages).toHaveLength(1);
    feed.stop();
  });
});
