import { EventStore, type Scheduler } from '../state/eventStore';
import type { DrydockEvent } from '../api/types';
import type { EventStream, StreamHandlers, Transport } from '../api/transport';
import type { Client } from '../api/client';
import type { AppServices } from '../state/context';

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

export function services(client: Partial<Client>, store?: EventStore): AppServices {
  return { client: client as Client, store: store ?? new EventStore(new FakeTransport()), transportName: 'fake' };
}
