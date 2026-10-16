import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { LocalBackend, LocalModel, LocalSettingsEdit, LocalView, Variant } from '../../api/localClient.ts';
import { cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { LocalModels } from './local-models.tsx';

afterEach(cleanup);

const GiB = 2 ** 30;
const MiB = 2 ** 20;

const OFFICIAL = { id: 'official', name: 'llama.cpp (official weekly build)', repo: 'ggml-org/llama.cpp', cacheTypes: ['q8_0', 'q4_0', 'f16'] };
const FORK = {
  id: 'turboquant',
  name: 'TurboQuant fork (AtomicBot-ai/atomic-llama-cpp-turboquant)',
  repo: 'AtomicBot-ai/atomic-llama-cpp-turboquant',
  warning: 'A community fork of llama.cpp that Drydock has not reviewed.',
  cacheTypes: ['turbo4', 'turbo3', 'q8_0', 'q4_0', 'f16'],
};
const BACKENDS: LocalView['backends'] = [
  { id: 'vulkan', label: 'Vulkan', for: 'Any recent GPU (AMD, Intel, NVIDIA) through its Vulkan driver' },
  { id: 'cuda', label: 'CUDA', for: 'NVIDIA GPUs with a driver from 2025 or newer (CUDA 13)' },
  { id: 'cpu', label: 'CPU only', for: 'No GPU: slow, works everywhere' },
];

/** A labelled test double of the core's local.get answer: a supported PC with nothing installed. */
const view = (over: Partial<LocalView> = {}): LocalView => ({
  supported: true,
  runtime: { installed: false, devices: { probed: false, devices: [] } },
  sources: [OFFICIAL, FORK],
  backends: BACKENDS,
  suggestedBackend: 'cuda',
  models: [],
  downloads: [],
  server: { state: 'stopped' },
  settings: { source: 'official', backend: '', cacheType: 'q8_0', contextSize: 0, port: 9933, modelId: '', providerId: '', autostart: true },
  memory: { totalBytes: 32 * GiB, availableBytes: 20 * GiB, known: true },
  freeBytes: 120 * GiB,
  modelsBytes: 0,
  log: [],
  ...over,
});
const installed = (over: Partial<LocalView> = {}): LocalView =>
  view({
    runtime: {
      installed: true,
      source: 'official',
      sourceName: OFFICIAL.name,
      tag: 'b11669',
      backend: 'vulkan',
      installedAt: '2026-10-15T02:00:00Z',
      devices: { probed: true, devices: [{ id: 'CUDA0', kind: 'CUDA', name: 'NVIDIA GeForce RTX 4060 Ti', totalBytes: 16 * GiB, freeBytes: 14 * GiB }] },
    },
    settings: { ...view().settings, backend: 'vulkan' },
    ...over,
  });
const model = (over: Partial<LocalModel> = {}): LocalModel => ({
  id: 'ggml-org/gpt-oss-20b-GGUF:MXFP4',
  repo: 'ggml-org/gpt-oss-20b-GGUF',
  variant: 'MXFP4',
  files: ['C:\\m\\gpt-oss-20b-mxfp4.gguf'],
  sizeBytes: 12 * GiB,
  downloadedAt: '2026-10-15T03:00:00Z',
  fit: { target: 'gpu', fits: true, availableBytes: 14 * GiB },
  ...over,
});

function show(client: Partial<Client>) {
  const s = testServices(client);
  const r = mount(<LocalModels />, s);
  return { r, s };
}
const q = (r: ReturnType<typeof show>['r'], sel: string) => r.$(sel) as HTMLElement | null;
const need = (r: ReturnType<typeof show>['r'], sel: string) => {
  const el = q(r, sel);
  if (!el) throw new Error(`no ${sel}`);
  return el;
};
const sel = (r: ReturnType<typeof show>['r'], s: string) => need(r, s) as HTMLSelectElement;
/** The control once the action before it has ended (a busy section disables its controls). */
const enabled = <T extends HTMLButtonElement | HTMLInputElement>(r: ReturnType<typeof show>['r'], s: string) =>
  waitFor(() => {
    const el = need(r, s) as T;
    if (el.disabled) throw new Error(`${s} is disabled`);
    return el;
  }, r);

describe('Models on this PC', () => {
  it('on a PC where local models are not supported shows the reason only', async () => {
    const { r } = show({ localGet: async () => view({ supported: false, reason: 'Local models run on Windows only.' }) });
    await waitFor(() => expect(need(r, '[data-testid="local-runtime"]').textContent).toContain('Local models run on Windows only.'), r);
    expect(need(r, '[data-testid="local-models"]').textContent).toContain('Models on this PC');
    expect(q(r, '[data-testid="local-install"]')).toBe(null);
    expect(q(r, '[data-testid="local-server"]')).toBe(null);
  });

  it('not installed: the suggested backend is preselected, the fork shows its warning when chosen, and Install carries source and backend', async () => {
    const calls: [string, LocalBackend | ''][] = [];
    let state = view();
    const { r } = show({
      localGet: async () => state,
      localInstall: async (source = '', backend = '') => {
        calls.push([source, backend]);
        state = view({
          runtime: {
            installed: false,
            devices: { probed: false, devices: [] },
            job: { kind: 'install', target: 'llama.cpp (official weekly build) for Vulkan', state: 'running', phase: 'downloading b11669', file: 'llama-b11669-bin-win-vulkan-x64.zip', done: 25 * MiB, total: 100 * MiB, started: '' },
          },
        });
        return state;
      },
    });
    await waitFor(() => need(r, '[data-testid="local-install"]'), r);
    expect(need(r, '[data-testid="local-runtime"]').getAttribute('data-installed')).toBe('false');
    expect(sel(r, '[data-testid="local-backend"]').value).toBe('cuda');
    expect(sel(r, '[data-testid="local-backend"]').selectedOptions[0]!.textContent).toContain('suggested for this PC');
    expect(sel(r, '[data-testid="local-source"]').value).toBe('official');
    expect(q(r, '[data-testid="local-source-warning"]')).toBe(null);

    type(sel(r, '[data-testid="local-source"]'), 'turboquant');
    await waitFor(() => expect(need(r, '[data-testid="local-source-warning"]').textContent).toContain('community fork of llama.cpp that Drydock has not reviewed'), r);
    type(sel(r, '[data-testid="local-source"]'), 'official');
    await waitFor(() => expect(q(r, '[data-testid="local-source-warning"]')).toBe(null), r);
    type(sel(r, '[data-testid="local-backend"]'), 'vulkan');
    await waitFor(() => expect(need(r, '[data-testid="local-runtime"]').textContent).toContain('Any recent GPU'), r);

    await r.act(() => (need(r, '[data-testid="local-install"]') as HTMLButtonElement).click());
    await waitFor(() => expect(calls).toEqual([['official', 'vulkan']]), r);
    // the answer is shown at once: the install runs, with its progress
    const job = await waitFor(() => need(r, '[data-testid="local-job"]'), r);
    expect(job.getAttribute('data-state')).toBe('running');
    expect(job.querySelector('[role="progressbar"]')!.getAttribute('aria-valuenow')).toBe('25');
    expect(job.textContent).toContain('25 MB of 100 MB (25%), downloading b11669');
    expect(job.textContent).toContain('llama-b11669-bin-win-vulkan-x64.zip');
    expect((need(r, '[data-testid="local-install"]') as HTMLButtonElement).disabled).toBe(true);
  });

  it('a failed install shows its error with a Retry', async () => {
    const retried: unknown[] = [];
    const { r } = show({
      localGet: async () =>
        view({ runtime: { installed: false, devices: { probed: false, devices: [] }, job: { kind: 'install', target: 'llama.cpp for Vulkan', state: 'failed', error: 'digest mismatch', done: 0, total: 0, started: '' } } }),
      localInstall: async (...a) => (retried.push(a), view()),
    });
    const job = await waitFor(() => need(r, '[data-testid="local-job"][data-state="failed"]'), r);
    expect(job.querySelector('[role="alert"]')!.textContent).toContain('digest mismatch');
    await r.act(() => (need(r, '[data-testid="local-retry"]') as HTMLButtonElement).click());
    await waitFor(() => expect(retried).toEqual([['official', 'cuda']]), r);
  });

  it('installed: the tag, the backend, the source, the devices, and Update calls install with the settings', async () => {
    const calls: unknown[] = [];
    const { r } = show({
      localGet: async () => installed(),
      localInstall: async (...a) => (calls.push(a), installed({ runtime: { ...installed().runtime, job: { kind: 'install', target: 'x', state: 'done', phase: 'already the newest build (b11669)', done: 0, total: 0, started: '' } } })),
    });
    const rt = await waitFor(() => need(r, '[data-testid="local-runtime"][data-installed="true"]'), r);
    expect(rt.querySelector('h3')!.textContent).toBe('llama.cpp b11669 · Vulkan · llama.cpp (official weekly build)');
    expect(rt.textContent).toContain('installed');
    expect(need(r, '[data-testid="local-device"][data-id="CUDA0"]').textContent).toBe('CUDA0: NVIDIA GeForce RTX 4060 Ti, 14.0 GB free of 16.0 GB');
    expect(q(r, '[data-testid="local-install"]')).toBe(null);
    await r.act(() => (need(r, '[data-testid="local-update"]') as HTMLButtonElement).click());
    await waitFor(() => expect(calls).toEqual([['official', 'vulkan']]), r);
    await waitFor(() => expect(need(r, '[data-testid="local-job"]').textContent).toContain('already the newest build (b11669)'), r);
    expect(need(r, '[data-testid="local-memory"]').textContent).toBe('RAM: 20.0 GB free of 32.0 GB · Models on disk: 0 MB · Free disk: 120.0 GB');
  });

  it('devices: "No GPU found" when probed with none; the probe error when it did not answer', async () => {
    const none = show({ localGet: async () => installed({ runtime: { ...installed().runtime, devices: { probed: true, devices: [] } } }) });
    await waitFor(() => expect(need(none.r, '[data-testid="local-devices"]').textContent).toBe('No GPU found: runs on the CPU'), none.r);
    const err = show({ localGet: async () => installed({ runtime: { ...installed().runtime, devices: { probed: false, devices: [], error: 'the program printed no device list' } } }) });
    await waitFor(() => expect(need(err.r, '[data-testid="local-devices"]').textContent).toContain('the program printed no device list'), err.r);
  });

  it('models carry their size and fit; Serve starts the server with the model and the answer is shown; Stop stops it', async () => {
    const started: string[] = [];
    let stops = 0;
    const models = [
      model(),
      model({ id: 'ggml-org/Qwen3-Coder-30B-A3B-Instruct-GGUF:Q8_0', variant: 'Q8_0', sizeBytes: 32 * GiB, fit: { target: 'cpu', fits: true, availableBytes: 15 * GiB } }),
      model({ id: 'big/model:F16', variant: 'F16', sizeBytes: 80 * GiB, fit: { target: 'none', fits: false, availableBytes: 15 * GiB } }),
      model({ id: 'x/y:Q4', variant: 'Q4', sizeBytes: 500 * MiB, fit: { target: 'unknown', fits: true, availableBytes: 0 } }),
    ];
    let state = installed({ models, modelsBytes: 124.5 * GiB });
    const { r } = show({
      localGet: async () => state,
      localServerStart: async (id) => {
        started.push(id);
        state = installed({
          models,
          server: { state: 'running', modelId: id, port: 9933, baseUrl: 'http://127.0.0.1:9933/v1' },
          settings: { ...installed().settings, modelId: id, providerId: 'prov_local' },
          log: ['srv  info: model loaded', 'main: server is listening on http://127.0.0.1:9933'],
        });
        return state;
      },
      localServerStop: async () => {
        stops++;
        state = installed({ models, server: { state: 'stopped' }, settings: { ...installed().settings, providerId: 'prov_local' } });
        return state;
      },
    });
    await waitFor(() => expect(r.$$('[data-testid="local-model"]').length).toBe(4), r);
    const row = (id: string) => need(r, `[data-testid="local-model"][data-id="${id}"]`);
    expect(row(models[0]!.id).textContent).toContain('12.0 GB');
    expect(row(models[0]!.id).querySelector('[data-testid="local-fit"]')!.textContent).toBe('Fits the GPU');
    expect(row(models[1]!.id).querySelector('[data-testid="local-fit"]')!.textContent).toBe('Runs on the CPU');
    expect(row(models[2]!.id).querySelector('[data-testid="local-fit"]')!.textContent).toBe('Too big for this PC');
    expect(row(models[3]!.id).querySelector('[data-testid="local-fit"]')).toBe(null);
    expect(need(r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('stopped');
    expect(q(r, '[data-testid="local-server-stop"]')).toBe(null);

    await r.act(() => (row(models[0]!.id).querySelector('[data-testid="local-serve"]') as HTMLButtonElement).click());
    await waitFor(() => expect(started).toEqual([models[0]!.id]), r);
    await waitFor(() => expect(need(r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('running'), r);
    const server = need(r, '[data-testid="local-server"]');
    expect(server.querySelector('[data-testid="local-server-state"]')!.textContent).toBe('running');
    expect(server.querySelector('[data-testid="local-server-url"]')!.textContent).toBe('http://127.0.0.1:9933/v1');
    expect(server.textContent).toContain(models[0]!.id);
    expect(need(r, '[data-testid="local-provider-note"]').textContent).toContain("Registered as the provider 'Models on this PC (llama.cpp)' below.");
    expect(row(models[0]!.id).textContent).toContain('serving');
    // the model served cannot be served again; the others wait while nothing starts
    expect((row(models[0]!.id).querySelector('[data-testid="local-serve"]') as HTMLButtonElement).disabled).toBe(true);
    expect((row(models[1]!.id).querySelector('[data-testid="local-serve"]') as HTMLButtonElement).disabled).toBe(false);
    // the log opens on demand
    expect(q(r, '[data-testid="local-log-lines"]')).toBe(null);
    await r.act(() => (need(r, '[data-testid="local-log"]') as HTMLButtonElement).click());
    expect(need(r, '[data-testid="local-log-lines"]').textContent).toContain('server is listening');

    await r.act(() => (need(r, '[data-testid="local-server-stop"]') as HTMLButtonElement).click());
    await waitFor(() => expect(need(r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('stopped'), r);
    expect(stops).toBe(1);
  });

  it('a Serve button waits while the server starts; a failed server shows its error; Remove refused by the core shows the error', async () => {
    const { r } = show({
      localGet: async () => installed({ models: [model()], server: { state: 'starting', modelId: 'other:Q4' } }),
      localModelRemove: async () => {
        throw new Error('ggml-org/gpt-oss-20b-GGUF:MXFP4 is being served: stop the server first');
      },
    });
    const row = await waitFor(() => need(r, '[data-testid="local-model"]'), r);
    expect((row.querySelector('[data-testid="local-serve"]') as HTMLButtonElement).disabled).toBe(true);
    expect(need(r, '[data-testid="local-server-state"]').textContent).toBe('starting');
    await r.act(() => (row.querySelector('[data-testid="local-model-remove"]') as HTMLButtonElement).click());
    await waitFor(() => expect(need(r, '[role="alert"]').textContent).toContain('stop the server first'), r);

    const failed = show({ localGet: async () => installed({ server: { state: 'failed', error: 'port 9933 is in use' } }) });
    await waitFor(() => expect(need(failed.r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('failed'), failed.r);
    expect(need(failed.r, '[data-testid="local-server"] [role="alert"]').textContent).toContain('port 9933 is in use');
  });

  it('downloads: a running one has a bar, its file and Cancel; a finished one has Clear (the same call)', async () => {
    const cancelled: string[] = [];
    const { r } = show({
      localGet: async () =>
        installed({
          downloads: [
            { kind: 'download', target: 'acme/tiny-GGUF:Q4_0', state: 'running', file: 'tiny-Q4_0.gguf', done: 1 * MiB, total: 2 * MiB, started: '' },
            { kind: 'download', target: 'ggml-org/gpt-oss-20b-GGUF:MXFP4', state: 'done', done: 24 * MiB, total: 24 * MiB, started: '' },
            { kind: 'download', target: 'bad/one:Q2', state: 'failed', error: 'sha256 mismatch', done: 0, total: 5, started: '' },
          ],
        }),
      localModelDownloadCancel: async (id) => (cancelled.push(id), installed()),
    });
    await waitFor(() => expect(r.$$('[data-testid="local-download-row"]').length).toBe(3), r);
    const running = need(r, '[data-testid="local-download-row"][data-state="running"]');
    expect(running.querySelector('[role="progressbar"]')!.getAttribute('aria-valuenow')).toBe('50');
    expect(running.textContent).toContain('tiny-Q4_0.gguf');
    expect(running.querySelector('button')!.textContent).toBe('Cancel');
    const done = need(r, '[data-testid="local-download-row"][data-state="done"]');
    expect(done.querySelector('button')!.textContent).toBe('Clear');
    expect(need(r, '[data-testid="local-download-row"][data-state="failed"]').textContent).toContain('sha256 mismatch');
    await r.act(() => running.querySelector('button')!.click());
    await waitFor(() => expect(cancelled).toEqual(['acme/tiny-GGUF:Q4_0']), r);
    await waitFor(() => expect(r.$$('[data-testid="local-download-row"]').length).toBe(0), r);
  });

  it('adds a model: suggestions on demand, a search, one repository open at a time with its variants, Download', async () => {
    const asked: string[] = [];
    const downloads: [string, string][] = [];
    const variants: Record<string, Variant[]> = {
      'ggml-org/gpt-oss-20b-GGUF': [{ name: 'MXFP4', files: [], sizeBytes: 12 * GiB, fit: { target: 'gpu', fits: true, availableBytes: 14 * GiB }, notes: ['the only quantization the model ships in'] }],
      'acme/tiny-GGUF': [{ name: 'Q4_0', files: [], sizeBytes: 2 * MiB, fit: { target: 'gpu', fits: true, availableBytes: 14 * GiB } }],
    };
    const { r } = show({
      localGet: async () => installed(),
      localModelsSuggested: async () => [
        { id: 'ggml-org/gpt-oss-20b-GGUF', downloads: 900000, likes: 10, updated: '', tags: ['reasoning'] },
        { id: 'ggml-org/Qwen3-Coder-30B-A3B-Instruct-GGUF', downloads: 700000, likes: 10, updated: '', tags: ['code'] },
      ],
      localModelsSearch: async (text) => (asked.push(text), [{ id: 'acme/tiny-GGUF', downloads: 10, likes: 0, updated: '', tags: [] }]),
      localModelVariants: async (repo) => variants[repo] ?? [],
      localModelDownload: async (repo, variant) => (downloads.push([repo, variant]), installed({ downloads: [{ kind: 'download', target: `${repo}:${variant}`, state: 'running', done: 0, total: 2 * MiB, started: '' }] })),
    });
    await waitFor(() => need(r, '[data-testid="local-suggested"]'), r);
    expect(q(r, '[data-testid="local-repos"]')).toBe(null);
    await r.act(() => (need(r, '[data-testid="local-suggested"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$$('[data-testid="local-repo"]').length).toBe(2), r);
    const first = need(r, '[data-testid="local-repo"][data-id="ggml-org/gpt-oss-20b-GGUF"]');
    expect(first.textContent).toContain('900k downloads');
    expect(first.textContent).toContain('reasoning');
    await r.act(() => first.click());
    const variant = await waitFor(() => need(r, '[data-testid="local-variant"][data-name="MXFP4"]'), r);
    expect(variant.textContent).toContain('12.0 GB');
    expect(variant.textContent).toContain('Fits the GPU');
    expect(variant.textContent).toContain('the only quantization');
    expect(first.getAttribute('aria-pressed')).toBe('true');

    // a search replaces the list; opening its repository closes the other
    type(need(r, '[data-testid="local-search-input"]') as HTMLInputElement, 'tiny');
    const go = await enabled<HTMLButtonElement>(r, '[data-testid="local-search"] button[type="submit"]');
    await r.act(() => go.click());
    await waitFor(() => expect(asked).toEqual(['tiny']), r);
    const tiny = await waitFor(() => need(r, '[data-testid="local-repo"][data-id="acme/tiny-GGUF"]'), r);
    expect(r.$$('[data-testid="local-repo"]').length).toBe(1);
    await r.act(() => tiny.click());
    const q4 = await waitFor(() => need(r, '[data-testid="local-variant"][data-name="Q4_0"]'), r);
    expect(r.$$('[data-testid="local-variant"]').length).toBe(1);
    await r.act(() => (q4.querySelector('[data-testid="local-download"]') as HTMLButtonElement).click());
    await waitFor(() => expect(downloads).toEqual([['acme/tiny-GGUF', 'Q4_0']]), r);
    await waitFor(() => need(r, '[data-testid="local-download-row"][data-id="acme/tiny-GGUF:Q4_0"]'), r);
  });

  it('each setting change sends just that field; a cache or context change while the server runs asks for a restart', async () => {
    const edits: LocalSettingsEdit[] = [];
    let state = installed({ server: { state: 'running', modelId: 'm', baseUrl: 'http://127.0.0.1:9933/v1' } });
    const { r } = show({
      localGet: async () => state,
      localSettingsSet: async (e) => {
        edits.push(e);
        state = { ...state, settings: { ...state.settings, ...e } as LocalView['settings'] };
        return state;
      },
    });
    await waitFor(() => need(r, '[data-testid="local-cache"]'), r);
    const cache = sel(r, '[data-testid="local-cache"]');
    expect(Array.from(cache.options).map((o) => o.value)).toEqual(['q8_0', 'q4_0', 'f16']);
    expect(cache.value).toBe('q8_0');
    expect(need(r, '[data-testid="local-settings"]').textContent).toContain('q8_0: near-lossless, half the KV memory of f16');
    expect(q(r, '[data-testid="local-restart"]')).toBe(null);

    type(cache, 'q4_0');
    await waitFor(() => expect(edits).toEqual([{ cacheType: 'q4_0' }]), r);
    await waitFor(() => expect(need(r, '[data-testid="local-restart"]').textContent).toContain('Restart the server to apply'), r);

    type(await enabled(r, '[data-testid="local-context"]'), '65536');
    await waitFor(() => expect(edits[1]).toEqual({ contextSize: 65536 }), r);
    type(await enabled(r, '[data-testid="local-port"]'), '9944');
    await waitFor(() => expect(edits[2]).toEqual({ port: 9944 }), r);
    const auto = await enabled<HTMLInputElement>(r, '[data-testid="local-autostart"] input');
    expect(auto.checked).toBe(true);
    await r.act(() => auto.click());
    await waitFor(() => expect(edits[3]).toEqual({ autostart: false }), r);
    expect(edits.length).toBe(4);
    // the same value again sends nothing
    type(await enabled(r, '[data-testid="local-port"]'), '9944');
    await waitFor(() => expect((need(r, '[data-testid="local-port"]') as HTMLInputElement).disabled).toBe(false), r);
    expect(edits.length).toBe(4);
  });

  it('the fork offers the TurboQuant cache types once it is the installed source', async () => {
    const { r } = show({ localGet: async () => installed({ runtime: { ...installed().runtime, source: 'turboquant', sourceName: FORK.name }, settings: { ...installed().settings, source: 'turboquant', cacheType: 'turbo4' } }) });
    await waitFor(() => need(r, '[data-testid="local-cache"]'), r);
    expect(Array.from(sel(r, '[data-testid="local-cache"]').options).map((o) => o.value)).toEqual(['turbo4', 'turbo3', 'q8_0', 'q4_0', 'f16']);
    expect(sel(r, '[data-testid="local-cache"]').value).toBe('turbo4');
  });

  it('the event local.changed reads the view again', async () => {
    let state = installed();
    let reads = 0;
    const { r, s } = show({
      localGet: async () => {
        reads++;
        return state;
      },
    });
    await waitFor(() => need(r, '[data-testid="local-runtime"][data-installed="true"]'), r);
    expect(reads).toBe(1);
    state = installed({ models: [model()], server: { state: 'running', modelId: model().id, baseUrl: 'http://127.0.0.1:9933/v1' } });
    s.emit({ type: 'local.changed', data: { what: 'server', state: 'running' } });
    await waitFor(() => expect(need(r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('running'), r);
    await waitFor(() => need(r, '[data-testid="local-model"]'), r);
    expect(reads).toBe(2);
    // an unrelated event does not
    s.emit({ type: 'provider.changed' });
    await new Promise((res) => setTimeout(res, 250));
    expect(reads).toBe(2);
  });
});
