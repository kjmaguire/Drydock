// TEST DOUBLES (labelled): a transport and a scheduler the tests drive by hand. Nothing here imitates an agent.
import type { Scheduler } from '../state/eventStore.ts';
import type { DrydockEvent, Message } from '../api/types.ts';
import type { EventStream, StreamHandlers, Transport } from '../api/transport.ts';

export const ev = (seq: number, type = 'x.y', extra: Partial<DrydockEvent> = {}): DrydockEvent => ({ seq, time: '2026-01-01T00:00:00Z', type, ...extra });

/** A transport whose event streams are driven by the test. */
export class FakeTransport implements Transport {
  readonly name = 'fake';
  opens: { after: number; h: StreamHandlers; closed: boolean }[] = [];
  calls: { method: string; params: unknown }[] = [];
  responses = new Map<string, unknown>();
  async call<T>(method: string, params?: unknown): Promise<T> {
    this.calls.push({ method, params });
    return (this.responses.get(method) ?? null) as T;
  }
  openEventStream(after: number, h: StreamHandlers): EventStream {
    const rec = { after, h, closed: false };
    this.opens.push(rec);
    return {
      close: () => {
        rec.closed = true;
      },
    };
  }
  get current() {
    return this.opens[this.opens.length - 1]!;
  }
}

/** A scheduler the test advances by hand. */
export class ManualScheduler implements Scheduler {
  private n = 0;
  tasks = new Map<number, { fn: () => void; ms: number }>();
  setTimeout(fn: () => void, ms: number): unknown {
    const id = ++this.n;
    this.tasks.set(id, { fn, ms });
    return id;
  }
  clearTimeout(h: unknown): void {
    this.tasks.delete(h as number);
  }
  /** Run everything currently scheduled. */
  runAll(): number[] {
    const ran: number[] = [];
    for (const [id, t] of [...this.tasks]) {
      this.tasks.delete(id);
      ran.push(t.ms);
      t.fn();
    }
    return ran;
  }
}

/** One read of the room.messages double: forward (afterSeq), the newest page, or the page before a seq. */
export type RoomRead = ['after', number, number] | ['latest', number] | ['before', number, number];

/**
 * TEST DOUBLE (labelled): room.messages as the core answers it (internal/room/post.go), over the list `all()` returns.
 * Forward: seq > afterSeq, oldest first, at most limit. Latest: the newest limit. Before: the limit just before beforeSeq.
 * Backward reads are oldest first too. Every read is recorded in `calls`.
 */
export function roomMessagesDouble(all: () => Message[]) {
  const calls: RoomRead[] = [];
  const back = (before: number, limit: number) => {
    const older = all().filter((m) => m.seq < before);
    return older.slice(Math.max(0, older.length - limit));
  };
  return {
    calls,
    roomMessages: async (_roomId: string, afterSeq = 0, limit = 200): Promise<Message[]> => {
      calls.push(['after', afterSeq, limit]);
      return all()
        .filter((m) => m.seq > afterSeq)
        .slice(0, limit);
    },
    roomMessagesLatest: async (_roomId: string, limit = 200): Promise<Message[]> => {
      calls.push(['latest', limit]);
      return back(Number.MAX_SAFE_INTEGER, limit);
    },
    roomMessagesBefore: async (_roomId: string, beforeSeq: number, limit = 200): Promise<Message[]> => {
      calls.push(['before', beforeSeq, limit]);
      return back(beforeSeq, limit);
    },
  };
}
