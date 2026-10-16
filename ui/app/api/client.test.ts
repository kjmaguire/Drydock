import { expect } from 'remix/assert';
import { describe, it, mock } from 'remix/test';
import { createClient } from './client.ts';
import { HttpTransport, RpcError, parseEvent, resolveToken, type EventSourceLike } from './transport.ts';
import { BRIDGE_FQN, bridgeFromRuntime, isWailsHost, loadWailsRuntime, WailsTransport } from './wails.ts';

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const text = (body: string, status: number) => new Response(body, { status, headers: { 'Content-Type': 'text/plain' } });

function transport(fetchImpl: typeof fetch) {
  return new HttpTransport({ token: 'tok', fetchImpl });
}

describe('HttpTransport.call', () => {
  it('posts JSON with the token header and unwraps the result', async () => {
    const f = mock.fn(async () => json({ result: { id: 'p1' } }));
    const out = await transport(f as unknown as typeof fetch).call('project.get', { id: 'p1' });
    expect(out).toEqual({ id: 'p1' });
    const [url, init] = f.mock.calls.map((c) => c.arguments)[0] as unknown as [string, RequestInit];
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
    expect(await c.roomMessagesLatest('r')).toEqual([]);
    expect(await c.roomMessagesBefore('r', 5)).toEqual([]);
    expect(await c.githubIssues('p')).toEqual([]);
    expect(await c.taskDiff('t')).toBe('');
    const d = await c.taskGet('t');
    expect(d.evidence).toEqual([]);
    expect(d.reviews).toEqual([]);
    expect(d.pending).toEqual([]);
  });

  it('deletes a project with the typed name, and fills a missing warnings list', async () => {
    const f = mock.fn(async () => json({ result: { name: 'gocalc', worktreesRemoved: 1, warnings: null } }));
    const c = createClient(transport(f as unknown as typeof fetch));
    expect(await c.projectDelete('p', 'gocalc')).toEqual({ name: 'gocalc', worktreesRemoved: 1, warnings: [] });
    const [url, init] = f.mock.calls.map((c) => c.arguments)[0] as unknown as [string, RequestInit];
    expect([url, JSON.parse(init.body as string)]).toEqual(['/rpc/project.delete', { id: 'p', confirmName: 'gocalc' }]);
  });

  it('sends the parameter names the core decodes', async () => {
    const f = mock.fn(async () => json({ result: null }));
    const c = createClient(transport(f as unknown as typeof fetch));
    await c.runRespond('run1', 'req1', 'rejected', { feedback: 'more tests' });
    await c.roomPost('room1', 'hello');
    await c.projectUpdate('p', 'pr_flow', 'squash');
    await c.githubImportIssue('p', 7);
    await c.roomMessages('room1', 4, 10);
    await c.roomMessagesLatest('room1', 101);
    await c.roomMessagesBefore('room1', 51, 101);
    await c.projectGitConfig('p');
    await c.projectTrustGitConfig('p', 'fp1');
    const bodies = f.mock.calls.map((c) => c.arguments).map((c) => [(c as unknown as [string])[0], JSON.parse((c as unknown as [string, RequestInit])[1].body as string)]);
    expect(bodies).toEqual([
      ['/rpc/run.respond', { runId: 'run1', requestId: 'req1', decision: 'rejected', data: { feedback: 'more tests' } }],
      ['/rpc/room.post', { roomId: 'room1', body: 'hello', threadRoot: '' }],
      ['/rpc/project.update', { id: 'p', mode: 'pr_flow', mergeStrategy: 'squash' }],
      ['/rpc/github.importIssue', { projectId: 'p', number: 7 }],
      ['/rpc/room.messages', { roomId: 'room1', afterSeq: 4, limit: 10 }],
      ['/rpc/room.messages', { roomId: 'room1', latest: true, limit: 101 }],
      ['/rpc/room.messages', { roomId: 'room1', beforeSeq: 51, limit: 101 }],
      ['/rpc/project.gitConfig', { id: 'p' }],
      ['/rpc/project.trustGitConfig', { id: 'p', fingerprint: 'fp1' }],
    ]);
  });

  it('never sends a backward read that the core would take as a forward read from the start', async () => {
    const f = mock.fn(async () => json({ result: [] }));
    const c = createClient(transport(f as unknown as typeof fetch));
    const err = await c.roomMessagesBefore('r', 0).catch((e: unknown) => e);
    expect((err as Error).message).toContain('beforeSeq must be at least 1');
    expect(f.mock.calls).toHaveLength(0);
  });

  it('reads the git configuration status with an empty list for Go nil', async () => {
    const c = createClient(transport((async () => json({ result: { trusted: true, changed: null, fingerprint: 'fp' } })) as unknown as typeof fetch));
    expect(await c.projectGitConfig('p')).toEqual({ trusted: true, changed: [], fingerprint: 'fp' });
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
      close: mock.fn(),
    };
    const t = new HttpTransport({ token: 'a b', eventSourceFactory: (u) => (urls.push(u), es) });
    const got: number[] = [];
    let opened = 0;
    let errors = 0;
    const stream = t.openEventStream(12, { onOpen: () => opened++, onEvent: (e) => got.push(e.seq), onError: () => errors++ });
    expect(urls[0]).toBe('/events?after=12&token=a%20b');
    es.onopen?.({});
    listeners['drydock']!({ data: '{"seq":13,"type":"x"}' });
    const warnings: unknown[][] = [];
    const warn = console.warn;
    console.warn = (...a: unknown[]) => void warnings.push(a);
    try {
      listeners['drydock']!({ data: 'garbage' });
      listeners['drydock']!({ data: '{"seq":"15","type":"x","data":{"token":"secret-token"}}' });
    } finally {
      console.warn = warn;
    }
    // L43: what is not an event is ignored with a warning in the console that does not repeat its data
    expect(warnings).toHaveLength(2);
    expect(String(warnings[0]![0])).toContain('malformed event');
    expect(JSON.stringify(warnings)).not.toContain('secret-token');
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
    const Call = mock.fn(async () => '{"id":"p"}');
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
    const Subscribe = mock.fn(async () => undefined);
    const off = mock.fn();
    const t = new WailsTransport({ Call: async () => 'null', Subscribe }, { On: (_n, c) => ((cb = c), off) });
    const got: number[] = [];
    const s = t.openEventStream(7, { onOpen: () => undefined, onEvent: (e) => got.push(e.seq), onError: () => undefined });
    expect(Subscribe).toHaveBeenCalledWith(7);
    cb({ data: { seq: 8, type: 'a' } });
    cb({ data: [{ seq: 9, type: 'a' }] });
    const warn = console.warn;
    console.warn = () => {}; // the junk is warned about (see wails.test.ts)
    try {
      cb({ data: 'junk' });
    } finally {
      console.warn = warn;
    }
    expect(got).toEqual([8, 9]);
    s.close();
    expect(off).toHaveBeenCalled();
  });
});

describe('round 2 client methods', () => {
  it('sends the parameter names the core decodes and normalizes nulls', async () => {
    const f = mock.fn(async (url: string) => {
      if (url.endsWith('events.head')) return json({ result: 77 });
      if (url.endsWith('agent.get')) return json({ result: { agent: { id: 'a' }, runs: null, usage: null } });
      if (url.endsWith('connection.test')) return json({ result: { adapter: 'x', steps: null, matrix: null, summary: 's', ok: true } });
      if (url.endsWith('provider.test')) return json({ result: { ProviderID: 'p', OK: true, Models: null, Warnings: null } });
      return json({ result: null });
    });
    const c = createClient(transport(f as unknown as typeof fetch));
    expect(await c.eventsHead()).toBe(77);
    expect(await c.agentGet('a')).toMatchObject({ runs: [], usage: [] });
    expect(await c.connectionTest('x')).toMatchObject({ steps: [], matrix: {} });
    expect(await c.providerTest('p')).toMatchObject({ Models: [], Warnings: [] });
    expect(await c.connectionList()).toEqual([]);
    expect(await c.providerList()).toEqual([]);
    expect(await c.providerModels('p')).toEqual([]);
    expect(await c.roomMembers('r')).toEqual([]);
    expect(await c.roomThread('m')).toEqual([]);
    await c.roomList('p', true);
    await c.roomGet('r1');
    await c.roomCreateDM('p', 'a1');
    await c.usageList({ taskId: 't' });
    await c.connectionAcknowledge('g');
    await c.connectionCheck('claude', 'Ubuntu');
    await c.providerSetEnabled('p', false);
    await c.providerDelete('p');
    await c.providerCreate({ Name: 'n', Kind: 'direct', BaseURL: 'https://x', DataClass: 'private', Tier: 'cheap' });
    const sent = f.mock.calls.map((c) => c.arguments).map((x) => [(x as unknown as [string])[0].replace('/rpc/', ''), JSON.parse((x as unknown as [string, RequestInit])[1].body as string)]);
    const by = (m: string) => sent.find((x) => x[0] === m)![1];
    expect(by('room.list')).toEqual({ projectId: 'p', includeArchived: true });
    expect(by('room.get')).toEqual({ id: 'r1' });
    expect(by('room.createDM')).toEqual({ projectId: 'p', agentId: 'a1' });
    expect(by('usage.list')).toEqual({ taskId: 't', agentId: '', limit: 0 });
    expect(by('connection.acknowledge')).toEqual({ gateId: 'g' });
    expect(by('connection.check')).toEqual({ adapter: 'claude', distro: 'Ubuntu' });
    expect(by('provider.setEnabled')).toEqual({ id: 'p', enabled: false });
    expect(by('provider.create')).toEqual({ Name: 'n', Kind: 'direct', BaseURL: 'https://x', DataClass: 'private', Tier: 'cheap' });
    expect(by('room.thread')).toEqual({ rootId: 'm' });
  });
});

describe('project setup client methods', () => {
  it('sends the parameter names the core decodes and normalizes nulls', async () => {
    const f = mock.fn(async (url: string) => {
      if (url.endsWith('project.setup')) return json({ result: { project: { id: 'p1' }, agents: null, warnings: null } });
      if (url.endsWith('agentcatalog.list')) return json({ result: [{ id: 'team-lead', alternatives: null, mcp: null, skills: null, tags: null, availability: { adapters: null, mcp: null, skills: null } }] });
      if (url.endsWith('agentcatalog.apply')) return json({ result: { id: 'team-lead', agent: { id: 'a1' }, mcp: { granted: null, skipped: null }, skills: { assigned: null }, notes: null } });
      if (url.endsWith('mcplib.catalog')) return json({ result: [{ id: 'cf', readOnlyTools: null, recommendedRoles: null }] });
      return json({ result: null });
    });
    const c = createClient(transport(f as unknown as typeof fetch));
    const req = { name: 'x', brief: '', repo: { kind: 'none' as const }, mode: 'local_merge' as const, libraryServers: [], catalogServers: ['cf'], agents: [{ prebuilt: 'team-lead' }], installSkills: true };
    expect(await c.projectSetup(req)).toEqual({ project: { id: 'p1' } as never, agents: [], warnings: [] });
    expect(await c.githubOwners()).toEqual([]);
    expect(await c.githubRepos()).toEqual([]);
    expect(await c.githubCloneDir()).toBe('');
    expect((await c.agentCatalogList('p1'))[0]).toMatchObject({ alternatives: [], mcp: [], skills: [], tags: [], availability: { adapters: [], preferredUsable: false, mcp: [], skills: [] } });
    expect(await c.agentCatalogApply({ id: 'team-lead', projectId: 'p1', installSkills: true, connectMcp: true })).toMatchObject({
      mcp: { granted: [], skipped: [] },
      skills: { assigned: [], shared: [], pending: [], failed: [] },
      notes: [],
    });
    expect((await c.mcpCatalog())[0]).toMatchObject({ readOnlyTools: [], recommendedRoles: [] });
    await c.projectCreate('later');
    await c.projectAttachRepo('p1', '/r');
    await c.projectSetBrief('p1', 'b');
    await c.githubRepos('acme', 'pay');
    await c.githubClone('acme/pay', '/src');
    await c.mcpAddFromCatalog('cf');
    const sent = f.mock.calls.map((c) => c.arguments).map((x) => [(x as unknown as [string])[0].replace('/rpc/', ''), JSON.parse((x as unknown as [string, RequestInit])[1].body as string)]);
    const all = (m: string) => sent.filter((x) => x[0] === m).map((x) => x[1]);
    expect(all('project.setup')).toEqual([req]);
    expect(all('project.create')).toEqual([{ name: 'later', repoPath: '' }]);
    expect(all('project.attachRepo')).toEqual([{ id: 'p1', repoPath: '/r' }]);
    expect(all('project.setBrief')).toEqual([{ id: 'p1', brief: 'b' }]);
    expect(all('github.repos')).toEqual([{ owner: '', query: '' }, { owner: 'acme', query: 'pay' }]);
    expect(all('github.clone')).toEqual([{ repo: 'acme/pay', dir: '/src' }]);
    expect(all('agentcatalog.list')).toEqual([{ projectId: 'p1' }]);
    expect(all('agentcatalog.apply')).toEqual([{ id: 'team-lead', projectId: 'p1', installSkills: true, connectMcp: true }]);
    expect(all('mcplib.addFromCatalog')).toEqual([{ catalogId: 'cf' }]);
  });
});

describe('team lead client methods', () => {
  it('sends the parameter names the core decodes and normalizes nulls', async () => {
    const f = mock.fn(async (url: string) => {
      if (url.endsWith('lead.interventions')) return json({ result: [{ id: 'i1', createdTasks: null }] });
      if (url.endsWith('lead.standup')) return json({ result: { messageId: 'm1', text: 'stand-up', tasks: 2 } });
      return json({ result: null });
    });
    const c = createClient(transport(f as unknown as typeof fetch));
    expect(await c.leadStandup('p1')).toEqual({ messageId: 'm1', text: 'stand-up', tasks: 2 });
    expect(await c.leadInterventions('p1')).toEqual([{ id: 'i1', createdTasks: [] } as never]);
    await c.leadInterventions('p1', 't1');
    const sent = f.mock.calls.map((c) => c.arguments).map((x) => [(x as unknown as [string])[0].replace('/rpc/', ''), JSON.parse((x as unknown as [string, RequestInit])[1].body as string)]);
    expect(sent).toEqual([
      ['lead.standup', { projectId: 'p1' }],
      ['lead.interventions', { projectId: 'p1', taskId: '' }],
      ['lead.interventions', { projectId: 'p1', taskId: 't1' }],
    ]);
  });
});

describe('Wails runtime wiring (against a fake runtime module; never run in WebView2)', () => {
  it('addresses the bridge by its fully qualified name and passes arguments in order', async () => {
    const ByName = mock.fn(async (name: string) => (name.endsWith('.Call') ? '{"ok":true}' : null));
    const On = mock.fn((_name: string, _cb: unknown) => () => undefined);
    const t = WailsTransport.fromRuntime({ Call: { ByName }, Events: { On } });
    expect(await t.call('project.list', {})).toEqual({ ok: true });
    expect(ByName).toHaveBeenCalledWith('github.com/kjmaguire/drydock/app.Bridge.Call', 'project.list', '{}');
    t.openEventStream(9, { onOpen: () => undefined, onEvent: () => undefined, onError: () => undefined });
    expect(ByName).toHaveBeenCalledWith('github.com/kjmaguire/drydock/app.Bridge.Subscribe', 9);
    expect(On.mock.calls[0]!.arguments[0]).toBe('drydock:event');
    expect(typeof On.mock.calls[0]!.arguments[1]).toBe('function');
    expect(BRIDGE_FQN).toBe('github.com/kjmaguire/drydock/app.Bridge');
    expect(typeof bridgeFromRuntime({ Call: { ByName }, Events: { On } }).Call).toBe('function');
  });
  it('detects the Wails host and refuses a runtime module that lacks the API', async () => {
    expect(isWailsHost({ chrome: { webview: {} } })).toBe(true);
    expect(isWailsHost({ _wails: {} })).toBe(true);
    expect(isWailsHost({})).toBe(false);
    expect(isWailsHost(undefined)).toBe(false);
    await expect(loadWailsRuntime(async () => ({}))).rejects.toThrow(/did not expose/);
    const rt = { Call: { ByName: async () => null }, Events: { On: () => () => undefined } };
    expect(await loadWailsRuntime(async (u) => (expect(u).toBe('/wails/runtime.js'), rt))).toBe(rt);
  });
});
