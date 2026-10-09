import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Client } from '../api/client';
import type { Message, Connection, ConnectionReport, Provider, ProviderModel, UsageRow } from '../api/types';
import { roomLabel } from '../lib/format';
import { AppServicesProvider } from '../state/context';
import { services } from '../test/fakes';
import { ConnectionsView, ReportView } from './Connections';
import { AgentInspector } from './Inspector';
import { ModelTable, priceText, ProbeView, ProvidersView } from './Providers';
import { ThreadPanel } from './Thread';
import { distinctSources, UsageTable } from './Usage';

const withServices = (ui: React.ReactElement, client: Partial<Client> = {}) => render(<AppServicesProvider services={services(client)}>{ui}</AppServicesProvider>);

const conn = (over: Partial<Connection> = {}): Connection => ({
  adapter: 'claude',
  installed: true,
  status: 'ready',
  usable: false,
  platform: 'windows',
  version: '2.1.0',
  gates: [{ id: 'claude-terms', text: 'Drydock uses your own unmodified claude login.', acked: false }],
  prereqs: [{ tool: 'node', min: '22', found: false, ok: false, guide: 'Install Node 22 from nodejs.org.' }],
  warnings: ['version not tested'],
  ...over,
});

const report = (over: Partial<ConnectionReport> = {}): ConnectionReport => ({
  adapter: 'claude',
  platform: 'windows',
  transport: 'headless',
  form: 'short',
  steps: [
    { id: 'T1', name: 'start', status: 'pass' },
    { id: 'T2', name: 'prompt', status: 'fail', reason: 'timeout', detail: 'no output in 30s' },
    { id: 'T3', name: 'interrupt', status: 'skipped', reason: 'T2 failed' },
  ],
  capabilities: {} as never,
  matrix: { streaming: { value: true, provenance: 'observed' }, mcp: { value: false, provenance: 'unknown' } },
  degraded: ['Interrupt could not be tested.'],
  summary: 'The CLI started but did not answer.',
  tokensUsed: 0,
  ok: false,
  elapsed: 31_500_000_000,
  ...over,
});

describe('Connections page', () => {
  it('shows the gate text and only the human can accept it: checkbox first, then Accept calls connection.acknowledge', async () => {
    const connectionAcknowledge = vi.fn(async () => undefined);
    withServices(<ConnectionsView />, { connectionList: async () => [conn()], connectionAcknowledge });
    await screen.findByText('Drydock uses your own unmodified claude login.');
    const accept = screen.getByRole('button', { name: 'Accept' }) as HTMLButtonElement;
    expect(accept.disabled).toBe(true);
    fireEvent.click(accept);
    expect(connectionAcknowledge).not.toHaveBeenCalled();
    fireEvent.click(screen.getByLabelText('I have read this and accept it'));
    fireEvent.click(accept);
    await waitFor(() => expect(connectionAcknowledge).toHaveBeenCalledWith('claude-terms'));
  });

  it('shows status, usable, warnings, prerequisites with guidance, and an accepted gate without a button', async () => {
    withServices(<ConnectionsView />, {
      connectionList: async () => [conn({ usable: true, gates: [{ id: 'g', text: 'notice', acked: true }], authenticated: null, installHint: 'winget install X' })],
    });
    await screen.findByTestId('connection');
    expect(screen.getByText('usable')).toBeTruthy();
    expect(screen.getByText('accepted')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
    expect(screen.getByText('version not tested')).toBeTruthy();
    expect(screen.getByText(/Install Node 22 from nodejs.org/)).toBeTruthy();
    expect(screen.getByText('not reported')).toBeTruthy(); // signed in unknown
    expect(screen.getByText(/never runs this for you/)).toBeTruthy();
  });

  it('Test connection warns about the real CLI and cost first, runs only after confirmation, and shows a plain report', async () => {
    const connectionTest = vi.fn(async () => report());
    withServices(<ConnectionsView />, { connectionList: async () => [conn()], connectionTest });
    await screen.findByTestId('connection');
    fireEvent.click(screen.getByRole('button', { name: 'Test connection...' }));
    expect(connectionTest).not.toHaveBeenCalled();
    expect(screen.getByText(/can start the real claude CLI/)).toBeTruthy();
    expect(screen.getByText(/cost a few cents/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Run the test' }));
    await screen.findByTestId('connection-report');
    expect(connectionTest).toHaveBeenCalledWith('claude');
    expect(screen.getByText('The CLI started but did not answer.')).toBeTruthy();
    expect(screen.getByText('did not pass')).toBeTruthy();
  });

  it('report: steps, degraded list, matrix provenance, tokens not reported', () => {
    const { container } = render(<ReportView r={report()} />);
    expect(container.textContent).toContain('T2 prompt: fail (timeout)');
    expect(container.textContent).toContain('no output in 30s');
    expect(container.textContent).toContain('Interrupt could not be tested.');
    expect(container.textContent).toContain('31.5 s');
    expect(container.textContent).toContain('Tokens used by the test: not reported');
    expect(container.textContent).toContain('observed');
    expect(container.textContent).toContain('unknown');
  });

  it('shows the core refusal when acknowledging fails', async () => {
    withServices(<ConnectionsView />, {
      connectionList: async () => [conn()],
      connectionAcknowledge: async () => {
        throw new Error('unknown gate');
      },
    });
    await screen.findByTestId('gate');
    fireEvent.click(screen.getByLabelText('I have read this and accept it'));
    fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
    await screen.findByText(/unknown gate/);
  });
});

const provider = (over: Partial<Provider> = {}): Provider => ({
  id: 'prov_1',
  name: 'OpenRouter',
  kind: 'aggregator',
  baseUrl: 'https://openrouter.ai/api/v1',
  keyRef: 'ref',
  dataClass: 'private',
  tier: 'cheap',
  flavor: 'openrouter',
  enabled: true,
  maxConcurrent: 0,
  hourlyCost: 0,
  tlsMode: 'system',
  lanUnencrypted: false,
  wireApis: null,
  status: 'up',
  lastSeen: '0001-01-01T00:00:00Z',
  createdAt: '',
  updatedAt: '',
  ...over,
});

const model = (over: Partial<ProviderModel>): ProviderModel => ({
  providerId: 'prov_1',
  id: 'm1',
  contextLength: 0,
  maxOutput: 0,
  supportsTools: false,
  supportsReasoning: false,
  dataClass: 'private',
  tier: 'cheap',
  isFree: false,
  inputPerM: 0,
  outputPerM: 0,
  cacheReadPerM: 0,
  cacheWritePerM: 0,
  priceSource: 'catalog',
  source: 'listing',
  ...over,
});

describe('Providers page', () => {
  it('lists providers; the key is never shown; a zero time is "never"; trainable carries the persistent notice', async () => {
    withServices(<ProvidersView />, {
      providerList: async () => [provider(), provider({ id: 'p2', name: 'FreeTier', dataClass: 'trainable', keyRef: '', status: 'unknown' })],
    });
    await screen.findAllByTestId('provider');
    expect(screen.getAllByText('never').length).toBe(2);
    expect(screen.getByText(/stored in the credential store \(never shown\)/)).toBeTruthy();
    expect(screen.getByTestId('trainable-note').textContent).toContain('Public documentation questions only; never receives repo content.');
  });

  it('create sends exported Go field names, the API key leaves the input immediately, and the key is not rendered anywhere', async () => {
    const providerCreate = vi.fn(async (_p: unknown) => provider());
    const { container } = withServices(<ProvidersView />, { providerList: async () => [], providerCreate });
    await screen.findByText('No providers yet.');
    fireEvent.change(screen.getByLabelText('Provider name'), { target: { value: 'My Host' } });
    fireEvent.change(screen.getByLabelText('Kind'), { target: { value: 'host' } });
    fireEvent.change(screen.getByLabelText('Base URL'), { target: { value: 'http://192.168.1.5:8000/v1' } });
    const key = screen.getByLabelText(/API key/) as HTMLInputElement;
    expect(key.type).toBe('password');
    fireEvent.change(key, { target: { value: 'sk-secret-123' } });
    fireEvent.change(screen.getByLabelText('Data class'), { target: { value: 'private' } });
    fireEvent.change(screen.getByLabelText('Tier'), { target: { value: 'free' } });
    fireEvent.click(screen.getByLabelText(/accept plain http/));
    fireEvent.click(screen.getByRole('button', { name: 'Add provider' }));
    await waitFor(() => expect(providerCreate).toHaveBeenCalled());
    expect(providerCreate.mock.calls[0]![0]).toEqual({
      Name: 'My Host',
      Kind: 'host',
      BaseURL: 'http://192.168.1.5:8000/v1',
      APIKey: 'sk-secret-123',
      DataClass: 'private',
      Tier: 'free',
      Attestations: { lan_attested: true },
    });
    expect(key.value).toBe('');
    expect(container.innerHTML).not.toContain('sk-secret-123');
  });

  it('shows the backend validation error verbatim and still does not keep the key', async () => {
    const msg = 'providers: plain http is only allowed on loopback, link-local or private addresses; use https';
    const providerCreate = vi.fn(async () => {
      throw new Error(msg);
    });
    const { container } = withServices(<ProvidersView />, { providerList: async () => [], providerCreate });
    await screen.findByText('No providers yet.');
    fireEvent.change(screen.getByLabelText('Provider name'), { target: { value: 'x' } });
    fireEvent.change(screen.getByLabelText('Base URL'), { target: { value: 'http://example.com/v1' } });
    const key = screen.getByLabelText(/API key/) as HTMLInputElement;
    fireEvent.change(key, { target: { value: 'sk-secret-456' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add provider' }));
    await screen.findByText(new RegExp(msg.slice(0, 40)));
    expect(screen.getByRole('alert').textContent).toContain(msg);
    expect(key.value).toBe('');
    expect(container.innerHTML).not.toContain('sk-secret-456');
  });

  it('Test shows the probe, Models shows the catalog, enable/disable and delete (confirmed) call the core', async () => {
    const providerTest = vi.fn(async () => ({ ProviderID: 'prov_1', OK: false, AuthFailed: true, Latency: 42e6, Flavor: 'openrouter', Models: null, ServerInfo: null, Warnings: ['rate limited'], Error: 'HTTP 401' }));
    const providerModels = vi.fn(async () => [model({ id: 'a/b' })]);
    const providerSetEnabled = vi.fn(async () => undefined);
    const providerDelete = vi.fn(async () => undefined);
    withServices(<ProvidersView />, { providerList: async () => [provider()], providerTest, providerModels, providerSetEnabled, providerDelete });
    await screen.findByTestId('provider');
    fireEvent.click(screen.getByRole('button', { name: 'Test' }));
    await screen.findByTestId('probe');
    expect(screen.getByText('key rejected')).toBeTruthy();
    expect(screen.getByText('HTTP 401')).toBeTruthy();
    expect(screen.getByText('rate limited')).toBeTruthy();
    expect(screen.getByText(/42 ms/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Models' }));
    await screen.findByTestId('model');
    fireEvent.click(screen.getByRole('button', { name: 'Disable' }));
    await waitFor(() => expect(providerSetEnabled).toHaveBeenCalledWith('prov_1', false));
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(providerDelete).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Yes, delete OpenRouter' }));
    await waitFor(() => expect(providerDelete).toHaveBeenCalledWith('prov_1'));
  });

  it('model prices: unknown is "not reported", never 0; free is free; source badges', () => {
    const rows = [
      model({ id: 'unknown-price' }),
      model({ id: 'free-one', isFree: true, priceSource: 'builtin' }),
      model({ id: 'paid', inputPerM: 3, outputPerM: 15, priceSource: 'catalog', contextLength: 200000, supportsTools: true }),
      model({ id: 'mine', inputPerM: 0.5, outputPerM: 1.5, priceSource: 'user' }),
    ];
    render(<ModelTable models={rows} />);
    const tr = (id: string) => screen.getByText(id).closest('tr')!;
    expect(tr('unknown-price').textContent).toContain('not reported');
    expect(tr('unknown-price').textContent).toContain('no price');
    expect(tr('unknown-price').textContent).not.toContain('$0');
    expect(tr('free-one').textContent).toContain('free');
    expect(tr('free-one').textContent).toContain('builtin');
    expect(tr('paid').textContent).toContain('$3.00');
    expect(tr('paid').textContent).toContain('$15.00');
    expect(tr('paid').textContent).toContain('catalog');
    expect(tr('mine').textContent).toContain('user');
    expect(priceText(model({}), 'out')).toBe('not reported');
  });

  it('probe view handles null lists', () => {
    const { container } = render(<ProbeView r={{ ProviderID: 'p', OK: true, AuthFailed: false, Latency: 0, Flavor: '', Models: null, ServerInfo: null, Warnings: null, Error: '' }} />);
    expect(container.textContent).toContain('reachable');
    expect(container.textContent).toContain('0 model(s)');
  });
});

const usage = (over: Partial<UsageRow>): UsageRow => ({
  ts: 1_700_000_000_000,
  taskId: 't1',
  agentId: 'a1',
  provider: 'claude',
  model: 'm',
  tier: 'premium',
  inputTokens: 10,
  outputTokens: 5,
  cachedTokens: 2,
  cacheWriteTokens: 0,
  reasoningTokens: 0,
  costUsd: 0.0123,
  source: 'reported',
  cacheHit: false,
  ...over,
});

describe('usage rows and the agent inspector', () => {
  it('usage rows carry a source badge and an unreported cost is never 0', () => {
    render(<UsageTable rows={[usage({}), usage({ ts: 2, costUsd: 0, source: 'estimated' }), usage({ ts: 3, source: 'weird' })]} />);
    const rows = screen.getAllByTestId('usage-row');
    expect(rows[0]!.textContent).toContain('$0.01');
    expect(rows[0]!.textContent).toContain('reported');
    expect(rows[1]!.textContent).toContain('not reported');
    expect(rows[1]!.textContent).toContain('estimated');
    expect(rows[1]!.textContent).not.toContain('$0');
    expect(rows[2]!.textContent).toContain('weird');
    expect(distinctSources([usage({}), usage({ source: 'estimated' }), usage({})])).toEqual(['estimated', 'reported']);
  });

  it('inspector shows runs with PID and cwd (not reported when absent) and usage with sources', async () => {
    const agentGet = vi.fn(async () => ({
      agent: { id: 'a1', projectId: 'p', name: 'Worker', adapter: 'claude', role: 'backend', model: '', effort: '', providerId: '', permissionMode: 'acceptEdits', status: '' },
      runs: [
        { id: 'r1', taskId: 't1', pid: 4242, cwd: '/w/t1', status: 'completed', startedAt: 1_700_000_000_000, endedAt: 1_700_000_100_000 },
        { id: 'r2', taskId: '', pid: 0, cwd: '', status: 'failed', startedAt: 1_700_000_200_000, endedAt: 0, error: 'exit 2' },
      ],
      usage: [usage({})],
    }));
    const onMessage = vi.fn();
    withServices(<AgentInspector agentId="a1" presence={[{ agentId: 'a1', presence: 'Working', activity: 'tool: Write', since: '', updated: '', seq: 1 }]} onMessage={onMessage} />, { agentGet });
    await screen.findByTestId('inspector');
    const runs = screen.getAllByTestId('agent-run');
    expect(runs).toHaveLength(2);
    expect(runs[0]!.textContent).toContain('4242');
    expect(runs[0]!.textContent).toContain('/w/t1');
    expect(runs[1]!.textContent).toContain('not reported');
    expect(runs[1]!.textContent).toContain('not ended');
    expect(runs[1]!.textContent).toContain('exit 2');
    expect(screen.getByText('adapter default (not set)')).toBeTruthy();
    expect(screen.getByText(/Not routed/)).toBeTruthy();
    expect(screen.getByTestId('usage-row').textContent).toContain('reported');
    expect(screen.getByText('tool: Write')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Message Worker' }));
    expect(onMessage).toHaveBeenCalledWith('a1');
  });

  it('inspector says what to do when no agent is chosen', () => {
    withServices(<AgentInspector agentId={undefined} presence={[]} />);
    expect(screen.getByText(/press Inspect/)).toBeTruthy();
  });
});

describe('threads and rooms', () => {
  it('a thread reply is posted with the root id and the thread reloads', async () => {
    const roomThread = vi.fn(async (): Promise<Message[]> => [
      { id: 'root', room_id: 'r', seq: 1, type: 'CHAT', author: { kind: 'human', id: 'kyle' }, body: 'question <b>x</b>', trust: 'trusted', created_at: '2026-01-01T00:00:00Z' },
    ]);
    const roomPost = vi.fn(async () => ({}));
    const { container } = withServices(<ThreadPanel roomId="r" rootId="root" agents={[]} onClose={() => undefined} />, { roomThread, roomPost });
    await screen.findByTestId('thread-message');
    expect(container.querySelector('b')).toBeNull(); // inert text
    fireEvent.change(screen.getByLabelText('Reply in thread'), { target: { value: 'an answer' } });
    fireEvent.click(screen.getByRole('button', { name: 'Reply' }));
    await waitFor(() => expect(roomPost).toHaveBeenCalledWith('r', 'an answer', 'root'));
    await waitFor(() => expect(roomThread).toHaveBeenCalledTimes(2));
  });

  it('DM rooms are labelled with the agent name', () => {
    const agents = [{ id: 'agt_1', projectId: 'p', name: 'Backend #1', adapter: 'claude', role: 'backend', model: '', effort: '', providerId: '', permissionMode: '', status: '' }];
    expect(roomLabel({ kind: 'dm', name: 'dm:human:kyle+agt_1' }, agents)).toBe('Backend #1');
    expect(roomLabel({ kind: 'dm', name: 'dm:human:kyle+agt_zzz' }, agents)).toBe('agt_zzz');
    expect(roomLabel({ kind: 'channel', name: 'general' }, agents)).toBe('general');
  });
});
