import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { Message } from '../../api/types.ts';
import { EventStore } from '../../state/eventStore.ts';
import { FakeTransport, ManualScheduler } from '../../test/fakes.ts';
import { latestPage, MessageFeed } from './feed.ts';
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
  // TEST DOUBLE (labelled): room.messages as the core answers it: seq > afterSeq, oldest first, at most limit.
  const room = (n: number) => Array.from({ length: n }, (_, i) => msg({ seq: i + 1 }));
  const reader = (all: () => Message[]) => {
    const calls: [after: number, limit: number][] = [];
    const get = async (after: number, limit: number) => {
      calls.push([after, limit]);
      return all().filter((m) => m.seq > after).slice(0, limit);
    };
    return { calls, get };
  };
  const setup = (all: () => Message[], pageSize = 3, hint = () => 0) => {
    const t = new FakeTransport();
    const sched = new ManualScheduler();
    const store = new EventStore(t, { scheduler: sched, backoff: () => 10_000 });
    const r = reader(all);
    const client = { roomMessages: async (_room: string, after = 0, limit = 200) => r.get(after, limit) };
    const feed = new MessageFeed(client, store, 'r', pageSize, hint);
    return { t, sched, store, calls: r.calls, feed };
  };
  const tick = () => new Promise((r) => setTimeout(r, 0));
  const settle = async () => {
    for (let i = 0; i < 20; i++) await tick();
  };

  it('finds the newest page of a long room in a few small reads, without reading the history', async () => {
    for (const n of [0, 1, 99, 100, 101, 250, 1000, 5003, 12_345]) {
      const all = room(n);
      const r = reader(() => all);
      const { page, hasMore } = await latestPage(r.get, 100);
      expect(page.map((m) => m.seq)).toEqual(all.slice(-100).map((m) => m.seq));
      expect(hasMore).toBe(n > 100);
      // what was read: at most three pages of messages, the rest are one-message probes
      const read = r.calls.reduce((sum, [after, limit]) => sum + Math.min(limit, Math.max(0, n - after)), 0);
      expect(read <= 3 * 100 + r.calls.length).toBe(true);
      expect(r.calls.length <= 4 + 2 * Math.ceil(Math.log2(Math.max(2, n / 100)))).toBe(true);
    }
  });

  it('a good hint lands on the tail in one read; a hint past the end still finds it', async () => {
    const all = room(1000);
    const good = reader(() => all);
    expect((await latestPage(good.get, 100, 1000)).page.at(-1)!.seq).toBe(1000);
    expect(good.calls).toEqual([[900, 101]]);
    const stale = reader(() => all);
    const res = await latestPage(stale.get, 100, 50_000);
    expect(res.page.map((m) => m.seq)).toEqual(all.slice(-100).map((m) => m.seq));
    expect(res.hasMore).toBe(true);
  });

  it('copes with gaps in the seqs', async () => {
    const all = room(800).filter((m) => m.seq % 3 !== 0);
    const r = reader(() => all);
    const { page, hasMore } = await latestPage(r.get, 100, 10);
    expect(page.map((m) => m.seq)).toEqual(all.slice(-100).map((m) => m.seq));
    expect(hasMore).toBe(true);
  });

  it('shows the newest page first and reads older pages on demand, oldest first', async () => {
    const all = room(8);
    const { feed } = setup(() => all);
    feed.start();
    await settle();
    expect(feed.messages.map((m) => m.seq)).toEqual([6, 7, 8]);
    expect(feed.hasMore).toBe(true);
    expect(feed.loading).toBe(false);
    expect(await feed.loadOlder()).toBe(3);
    expect(feed.messages.map((m) => m.seq)).toEqual([3, 4, 5, 6, 7, 8]);
    expect(feed.hasMore).toBe(true);
    expect(await feed.loadOlder()).toBe(2);
    expect(feed.messages.map((m) => m.seq)).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
    expect(feed.hasMore).toBe(false);
    expect(await feed.loadOlder()).toBe(0);
    feed.stop();
  });

  it('extends on room.message_posted for this room only, and re-pulls when the stream reopens, not when it breaks', async () => {
    let have = room(2);
    const { t, sched, store, calls, feed } = setup(() => have);
    store.start();
    t.current.h.onOpen();
    feed.start();
    await settle();
    expect(feed.messages).toHaveLength(2);
    const before = calls.length;
    have = room(3);
    store.ingest({ seq: 1, time: '', type: 'room.message_posted', data: { room: 'other' } });
    await settle();
    expect(calls.length).toBe(before);
    store.ingest({ seq: 2, time: '', type: 'room.message_posted', data: { room: 'r' } });
    await settle();
    expect(feed.messages.map((m) => m.seq)).toEqual([1, 2, 3]);
    expect(calls.at(-1)).toEqual([2, 3]);
    // the stream breaks: nothing is read while the core is unreachable
    have = room(5);
    const atBreak = calls.length;
    t.current.h.onError(new Error('down'));
    await settle();
    expect(calls.length).toBe(atBreak);
    // it reopens: the feed reads what was posted meanwhile
    sched.runAll();
    t.current.h.onOpen();
    await settle();
    expect(calls.at(-1)).toEqual([3, 3]);
    expect(feed.messages.map((m) => m.seq)).toEqual([1, 2, 3, 4, 5]);
    feed.stop();
    store.stop();
  });

  it('after more than a page of news (a long outage) starts again from the newest page', async () => {
    let have = room(3);
    const { t, sched, store, feed } = setup(() => have);
    store.start();
    feed.start();
    await settle();
    expect(feed.messages.map((m) => m.seq)).toEqual([1, 2, 3]);
    have = room(20);
    t.current.h.onError(new Error('down'));
    sched.runAll();
    t.current.h.onOpen();
    await settle();
    expect(feed.messages.map((m) => m.seq)).toEqual([18, 19, 20]);
    expect(feed.hasMore).toBe(true);
    feed.stop();
    store.stop();
  });

  it('keeps the error and the messages it had', async () => {
    let fail = false;
    const { feed } = setup(() => {
      if (fail) throw new Error('core is down');
      return room(1);
    });
    feed.start();
    await settle();
    fail = true;
    await feed.pull();
    expect(feed.error?.message).toBe('core is down');
    expect(feed.messages).toHaveLength(1);
    feed.stop();
  });
});
