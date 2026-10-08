import { describe, expect, it, vi } from 'vitest';
import { createClient } from './client';
import { HttpTransport, RpcError, parseEvent, resolveToken, type EventSourceLike } from './transport';
import { WailsTransport } from './wails';

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const text = (body: string, status: number) => new Response(body, { status, headers: { 'Content-Type': 'text/plain' } });

function transport(fetchImpl: typeof fetch) {
  return new HttpTransport({ token: 'tok', fetchImpl });
}

describe('HttpTransport.call', () => {
  it('posts JSON with the token header and unwraps the result', async () => {
    const f = vi.fn(async () => json({ result: { id: 'p1' } }));
    const out = await transport(f as unknown as typeof fetch).call('project.get', { id: 'p1' });
    expect(out).toEqual({ id: 'p1' });
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/rpc/project.get');
    expect((init.headers as Record<string, string>)['X-Drydock-Token']).toBe('tok');
    expect(init.method).toBe('POST');
    expect(init.body).toBe('{"id":"p1"}');
    expect(init.credentials).toBe('omit');
  });

  it('turns the core error envelope into an RpcError carrying the core message', async () => {
    const t = transport((async () => json({ error: '/x is not a usable Git repository: not a git repository' }, 400)) as typeof fetch);
    const err = await t.call('project.create', {}).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RpcError);
    expect(err).toMatchObject({ kind: 'bad_request', status: 400, method: 'project.create', message: '/x is not a usable Git repository: not a git repository' });
  });

  it('classifies guard responses that are plain text', async () => {
    const mk = (status: number, body: string) => transport((async () => text(body, status)) as typeof fetch);
    expect(await mk(401, 'unauthorized\n').call('a').catch((e) => e)).toMatchObject({ kind: 'unauthorized', status: 401, message: 'unauthorized' });
    expect(await mk(403, 'forbidden').call('a').catch((e) => e)).toMatchObject({ kind: 'forbidden' });
    expect(await mk(404, '{"error":"unknown method \\"zzz\\""}').call('zzz').catch((e) => e)).toMatchObject({ kind: 'not_found', message: 'unknown method "zzz"' });
    expect(await mk(500, 'oops').call('a').catch((e) => e)).toMatchObject({ kind: 'server' });
  });

  it('reports a network failure without throwing a raw TypeError', async () => {
    const t = transport((async () => {
      throw new TypeError('Failed to fetch');
    }) as typeof fetch);
    const err = await t.call('app.info').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RpcError);
    expect(err).toMatchObject({ kind: 'network' });
    expect((err as Error).message).toContain('Failed to fetch');
  });

  it('rejects a 200 reply that is not the JSON envelope', async () => {
    const t = transport((async () => text('<html>not json</html>', 200)) as typeof fetch);
    expect(await t.call('app.info').catch((e) => e)).toMatchObject({ kind: 'invalid_response' });
  });

  it('treats an error field inside a 200 envelope as an error', async () => {
    const t = transport((async () => json({ error: 'nope' }, 200)) as typeof fetch);
    expect(await t.call('x').catch((e) => e)).toMatchObject({ kind: 'bad_request', message: 'nope' });
  });

  it('maps a missing result to null', async () => {
    const t = transport((async () => json({})) as typeof fetch);
    expect(await t.call('task.cancel')).toBeNull();
  });
});

describe('typed client', () => {
  it('turns null lists (Go nil slices) into empty arrays', async () => {
    const f = (async (url: string) => {
      if (url.includes('task.get')) return json({ result: { task: { id: 't' }, evidence: null, reviews: null, pending: null } });
      return json({ result: null });
    }) as unknown as typeof fetch;
    const c = createClient(transport(f));
    expect(await c.projectList()).toEqual([]);
    expect(await c.needsYouList('p')).toEqual([]);
    expect(await c.presenceList()).toEqual([]);
    expect(await c.roomMessages('r')).toEqual([]);
    expect(await c.githubIssues('p')).toEqual([]);
    expect(await c.taskDiff('t')).toBe('');
    const d = await c.taskGet('t');
    expect(d.evidence).toEqual([]);
    expect(d.reviews).toEqual([]);
    expect(d.pending).toEqual([]);
  });

  it('sends the parameter names the core decodes', async () => {
    const f = vi.fn(async () => json({ result: null }));
    const c = createClient(transport(f as unknown as typeof fetch));
    await c.runRespond('run1', 'req1', 'rejected', { feedback: 'more tests' });
    await c.roomPost('room1', 'hello');
    await c.projectUpdate('p', 'pr_flow', 'squash');
    await c.githubImportIssue('p', 7);
    const bodies = f.mock.calls.map((c) => [(c as unknown as [string])[0], JSON.parse((c as unknown as [string, RequestInit])[1].body as string)]);
    expect(bodies).toEqual([
      ['/rpc/run.respond', { runId: 'run1', requestId: 'req1', decision: 'rejected', data: { feedback: 'more tests' } }],
      ['/rpc/room.post', { roomId: 'room1', body: 'hello', threadRoot: '' }],
      ['/rpc/project.update', { id: 'p', mode: 'pr_flow', mergeStrategy: 'squash' }],
      ['/rpc/github.importIssue', { projectId: 'p', number: 7 }],
    ]);
  });

  it('propagates RpcError from the transport', async () => {
    const c = createClient(transport((async () => json({ error: 'the reviewer must differ from the worker' }, 400)) as typeof fetch));
    await expect(c.taskAssign('t', 'a', 'a')).rejects.toMatchObject({ kind: 'bad_request', message: 'the reviewer must differ from the worker' });
  });
});

describe('event stream parsing', () => {
  it('parses only well-formed events', () => {
    expect(parseEvent('{"seq":3,"type":"task.created","time":"t"}')).toMatchObject({ seq: 3 });
    expect(parseEvent('{"seq":0,"type":"a"}')).toBeUndefined();
    expect(parseEvent('{"seq":"3","type":"a"}')).toBeUndefined();
    expect(parseEvent('not json')).toBeUndefined();
    expect(parseEvent('null')).toBeUndefined();
  });

  it('HttpTransport opens /events with after and token and forwards drydock events', () => {
    const urls: string[] = [];
    const listeners: Record<string, (ev: { data: string }) => void> = {};
    const es: EventSourceLike = {
      onopen: null,
      onerror: null,
      addEventListener: (t, l) => {
        listeners[t] = l;
      },
      close: vi.fn(),
    };
    const t = new HttpTransport({ token: 'a b', eventSourceFactory: (u) => (urls.push(u), es) });
    const got: number[] = [];
    let opened = 0;
    let errors = 0;
    const stream = t.openEventStream(12, { onOpen: () => opened++, onEvent: (e) => got.push(e.seq), onError: () => errors++ });
    expect(urls[0]).toBe('/events?after=12&token=a%20b');
    es.onopen?.({});
    listeners['drydock']!({ data: '{"seq":13,"type":"x"}' });
    listeners['drydock']!({ data: 'garbage' });
    es.onerror?.({});
    expect({ opened, errors, got }).toEqual({ opened: 1, errors: 1, got: [13] });
    stream.close();
    listeners['drydock']!({ data: '{"seq":14,"type":"x"}' });
    expect(got).toEqual([13]);
    expect(es.close).toHaveBeenCalled();
  });
});

describe('resolveToken', () => {
  const mem = () => {
    const m = new Map<string, string>();
    return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
  };
  it('prefers the URL and remembers it for reloads', () => {
    const s = mem();
    expect(resolveToken('?token=abc', s)).toBe('abc');
    expect(resolveToken('', s)).toBe('abc');
  });
  it('works without storage and when storage throws', () => {
    expect(resolveToken('?token=abc', undefined)).toBe('abc');
    expect(resolveToken('', undefined)).toBe('');
    const bad = {
      getItem: () => {
        throw new Error('blocked');
      },
      setItem: () => {
        throw new Error('blocked');
      },
    };
    expect(resolveToken('?token=abc', bad)).toBe('abc');
    expect(resolveToken('', bad)).toBe('');
  });
});

describe('WailsTransport (against fakes; never run in WebView2)', () => {
  it('calls Bridge.Call with JSON strings and parses the result', async () => {
    const Call = vi.fn(async () => '{"id":"p"}');
    const t = new WailsTransport({ Call, Subscribe: async () => undefined }, { On: () => () => undefined });
    expect(await t.call('project.get', { id: 'p' })).toEqual({ id: 'p' });
    expect(Call).toHaveBeenCalledWith('project.get', '{"id":"p"}');
  });
  it('wraps bridge errors in RpcError', async () => {
    const t = new WailsTransport(
      {
        Call: async () => {
          throw new Error('core said no');
        },
        Subscribe: async () => undefined,
      },
      { On: () => () => undefined },
    );
    expect(await t.call('x').catch((e) => e)).toMatchObject({ name: 'RpcError', message: 'core said no' });
  });
  it('subscribes from a seq and forwards events from either payload shape', async () => {
    let cb: (e: { data: unknown }) => void = () => undefined;
    const Subscribe = vi.fn(async () => undefined);
    const off = vi.fn();
    const t = new WailsTransport({ Call: async () => 'null', Subscribe }, { On: (_n, c) => ((cb = c), off) });
    const got: number[] = [];
    const s = t.openEventStream(7, { onOpen: () => undefined, onEvent: (e) => got.push(e.seq), onError: () => undefined });
    expect(Subscribe).toHaveBeenCalledWith(7);
    cb({ data: { seq: 8, type: 'a' } });
    cb({ data: [{ seq: 9, type: 'a' }] });
    cb({ data: 'junk' });
    expect(got).toEqual([8, 9]);
    s.close();
    expect(off).toHaveBeenCalled();
  });
});
