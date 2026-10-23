import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Client } from '../../api/client.ts';
import type { LocalBackend, LocalModel, LocalSettingsEdit, LocalView, Variant } from '../../api/localClient.ts';
import { cleanup, drain, mount, optionsOf, testServices, type, valueOf, waitFor } from '../../test/render.tsx';
import { ModelsScreen } from './models-screen.tsx';

afterEach(cleanup);

const GiB = 2 ** 30;
const MiB = 2 ** 20;

const OFFICIAL = { id: 'official', name: 'Official weekly build', repo: 'ggml-org/llama.cpp', cacheTypes: ['q8_0', 'q4_0', 'f16'] };
const FORK = {
  id: 'turboquant',
  name: 'TurboQuant build',
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

const NOW = Date.parse('2026-10-22T12:00:00Z');

function show(client: Partial<Client>) {
  const s = testServices(client);
  const r = mount(<ModelsScreen now={NOW} />, s);
  return { r, s };
}
const q = (r: ReturnType<typeof show>['r'], sel: string) => r.$(sel) as HTMLElement | null;
const need = (r: ReturnType<typeof show>['r'], sel: string) => {
  const el = q(r, sel);
  if (!el) throw new Error(`no ${sel}`);
  return el;
};
const sel = (r: ReturnType<typeof show>['r'], s: string) => need(r, s) as HTMLElement;
const menuOpen = (r: ReturnType<typeof show>['r']) => Array.from(r.$$('[role="menu"]')).some((m) => m.closest('[popover]')?.matches(':popover-open'));
/** Open a menu by its trigger (a menu still finishing its last selection ignores a click, so it is repeated until it shows). */
async function openMenu(r: ReturnType<typeof show>['r'], trigger: HTMLElement) {
  await r.act(() => trigger.click());
  await waitFor(() => {
    if (!menuOpen(r) && trigger.getAttribute('aria-expanded') !== 'true') trigger.click();
    expect(menuOpen(r)).toBe(true);
  }, r);
}
/** Enter in the Hub search: the form is submitted (the mockup has no Search button). */
const submitSearch = (r: ReturnType<typeof show>['r']) => r.act(() => (need(r, '[data-testid="local-search"]') as HTMLFormElement).requestSubmit());
/** The control once the action before it has ended (a busy section disables its controls). */
const enabled = <T extends HTMLButtonElement | HTMLInputElement>(r: ReturnType<typeof show>['r'], s: string) =>
  waitFor(() => {
    const el = need(r, s) as T;
    if (el.disabled) throw new Error(`${s} is disabled`);
    return el;
  }, r);

describe('Models on this PC (the page)', () => {
  it('on a PC where local models are not supported shows the reason only', async () => {
    const { r } = show({ localGet: async () => view({ supported: false, reason: 'Local models run on Windows only.' }) });
    await waitFor(() => expect(need(r, '[data-testid="local-runtime"]').textContent).toContain('Local models run on Windows only.'), r);
    expect(need(r, '[data-testid="local-models"]').textContent).toContain('Models on this PC');
    expect(q(r, '[data-testid="local-install"]')).toBe(null);
    expect(q(r, '[data-testid="local-server"]')).toBe(null);
    expect(q(r, '[data-testid="local-search"]')).toBe(null);
  });

  // decision P95, on the owner's words: "I don't want to hit a button that says install llama.cpp, I don't even want it to say
  // llama.cpp". Drydock sets its engine up by itself; while it does, the card shows the progress and offers nothing to pick.
  it('not set up: the card names no llama.cpp and offers no source or backend; Set up asks for the TurboQuant build, and the setup shows its progress', async () => {
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
            job: { kind: 'install', target: 'TurboQuant build for Vulkan', state: 'running', phase: 'downloading b10298-2.0.0', file: 'llama-turboquant-windows-x64-vulkan.zip', done: 25 * MiB, total: 100 * MiB, started: '' },
          },
        });
        return state;
      },
    });
    await waitFor(() => need(r, '[data-testid="local-install"]'), r);
    const rt = need(r, '[data-testid="local-runtime"]');
    expect(rt.getAttribute('data-installed')).toBe('false');
    expect(need(r, '[data-testid="local-build"]').textContent).toBe('Local models are not set up');
    expect(need(r, '[data-testid="local-install"]').textContent).toBe('Set up');
    expect(rt.textContent).toContain('Drydock puts its model engine in place by itself when it starts.');
    expect(q(r, '[data-testid="local-backend"]')).toBe(null);
    expect(q(r, '[data-testid="local-source"]')).toBe(null);
    expect(need(r, '[data-testid="local-models"]').textContent!.toLowerCase()).not.toContain('llama');

    await r.act(() => (need(r, '[data-testid="local-install"]') as HTMLButtonElement).click());
    await waitFor(() => expect(calls).toEqual([['turboquant', '']]), r);
    // the answer is shown at once: the setup runs, with its progress, and there is nothing to press meanwhile
    const job = await waitFor(() => need(r, '[data-testid="local-job"]'), r);
    expect(job.getAttribute('data-state')).toBe('running');
    expect(job.querySelector('[role="progressbar"]')!.getAttribute('aria-valuenow')).toBe('25');
    expect(job.textContent).toContain('25 MB of 100 MB (25%), downloading b10298-2.0.0');
    expect(need(r, '[data-testid="local-build"]').textContent).toBe('Setting up local models');
    expect(need(r, '[data-testid="local-runtime"]').textContent).toContain("with Google's TurboQuant");
    expect(q(r, '[data-testid="local-install"]')).toBe(null);
  });

  it('a failed setup shows its error with a Retry, which asks for the TurboQuant build again', async () => {
    const retried: unknown[] = [];
    const { r } = show({
      localGet: async () =>
        view({ runtime: { installed: false, devices: { probed: false, devices: [] }, job: { kind: 'install', target: 'TurboQuant build for Vulkan', state: 'failed', error: 'digest mismatch', done: 0, total: 0, started: '' } } }),
      localInstall: async (...a) => (retried.push(a), view()),
    });
    const job = await waitFor(() => need(r, '[data-testid="local-job"][data-state="failed"]'), r);
    expect(job.querySelector('[role="alert"]')!.textContent).toContain('digest mismatch');
    expect(q(r, '[data-testid="local-install"]')).toBe(null);
    await r.act(() => (need(r, '[data-testid="local-retry"]') as HTMLButtonElement).click());
    await waitFor(() => expect(retried).toEqual([['turboquant', '']]), r);
  });

  it('installed: the build, the backend and the devices; no Update or Remove of the engine, only Check for updates', async () => {
    const { r } = show({ localGet: async () => installed() });
    const rt = await waitFor(() => need(r, '[data-testid="local-runtime"][data-installed="true"]'), r);
    expect(rt.querySelector('[data-testid="local-build"]')!.textContent).toBe('Official weekly build b11669 · Vulkan');
    expect(need(r, '[data-testid="local-build"]').getAttribute('title')).toContain('installed');
    expect(need(r, '[data-testid="local-runtime-note"]').textContent).toBe('Drydock set it up and keeps it current. An update restarts the server on the same model.');
    // the GPU is a bar in the Memory card: what is used of it, the device named in its tooltip
    const gpu = need(r, '[data-testid="local-device"][data-id="CUDA0"]');
    expect(gpu.querySelector('span[title]')!.getAttribute('title')).toBe('CUDA0: NVIDIA GeForce RTX 4060 Ti, 14.0 GB free of 16.0 GB');
    expect(gpu.textContent).toContain('GPU · CUDA');
    expect(gpu.querySelector('[data-testid="local-bar-used"]')!.textContent).toBe('2 of 16 GB');
    expect(gpu.querySelector('[role="img"]')!.getAttribute('aria-label')).toBe('GPU · CUDA: 2 of 16 GB used: other programs 2.0 GB');
    expect(gpu.querySelector('.legend')).toBe(null); // nothing served: no model in it
    expect(need(r, '[data-testid="local-ram"] [data-testid="local-bar-used"]').textContent).toBe('12 of 32 GB');
    expect(q(r, '[data-testid="local-install"]')).toBe(null);
    expect(q(r, '[data-testid="local-update"]')).toBe(null);
    expect(q(r, '[data-testid="local-runtime-remove"]')).toBe(null);
    expect(need(r, '[data-testid="local-check-updates"]').textContent).toBe('Check for updates');
    expect(need(r, '[data-testid="local-memory"]').textContent).toBe('Models on disk: 0 MB · Free disk: 120.0 GB');
  });

  it('devices: "No GPU found" when probed with none; the probe error when it did not answer', async () => {
    const none = show({ localGet: async () => installed({ runtime: { ...installed().runtime, devices: { probed: true, devices: [] } } }) });
    await waitFor(() => expect(need(none.r, '[data-testid="local-devices"]').textContent).toBe('No GPU found: runs on the CPU'), none.r);
    const err = show({ localGet: async () => installed({ runtime: { ...installed().runtime, devices: { probed: false, devices: [], error: 'the program printed no device list' } } }) });
    await waitFor(() => expect(need(err.r, '[data-testid="local-devices"]').textContent).toContain('the program printed no device list'), err.r);
  });

  it('models carry their name, quantization, repository, size and fit; Serve starts the server with the model and the answer is shown; Stop stops it', async () => {
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
    const fit = (id: string) => row(id).querySelector('[data-testid="local-fit"]');
    // the mockup's row: the model's name and quantization over its repository, the size, where it fits
    expect(row(models[0]!.id).querySelector('.name')!.textContent).toBe('gpt-oss-20b · MXFP4');
    expect(row(models[0]!.id).querySelector('.repo')!.textContent).toBe('ggml-org/gpt-oss-20b-GGUF');
    expect(row(models[0]!.id).querySelector('.size')!.textContent).toBe('12.0 GB');
    expect(fit(models[0]!.id)!.textContent).toBe('Fits · GPU');
    expect(fit(models[0]!.id)!.getAttribute('title')).toBe('Runs on the GPU (needs about 12.0 GB, 14.0 GB free).');
    // a model that does not fit the GPU alone runs on the GPU and in RAM; the badge stays short and carries the sentence as its tooltip
    expect(fit(models[1]!.id)!.textContent).toBe('Fits · GPU + RAM');
    expect(fit(models[1]!.id)!.getAttribute('data-tone')).toBe('warn');
    expect(fit(models[1]!.id)!.getAttribute('title')).toBe('Runs on the GPU and in RAM (slower): no GPU has room for all of it (needs about 32.0 GB, 15.0 GB free).');
    expect(fit(models[2]!.id)!.textContent).toBe('Too big for 15 GB');
    expect(fit(models[2]!.id)!.getAttribute('data-tone')).toBe('err');
    expect(fit(models[3]!.id)).toBe(null);
    expect(need(r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('stopped');
    expect(need(r, '[data-testid="local-server-title"]').textContent).toBe('Nothing served');
    expect(need(r, '[data-testid="local-server-state"]').getAttribute('aria-label')).toBe('stopped');
    // the KV cache the next start uses
    expect(need(r, '[data-testid="local-kv"]').textContent).toBe("q8_0 · the model's own context");
    expect(q(r, '[data-testid="local-server-stop"]')).toBe(null);
    expect(q(r, '[data-testid="local-server-url"]')).toBe(null);

    await r.act(() => (row(models[0]!.id).querySelector('[data-testid="local-serve"]') as HTMLButtonElement).click());
    await waitFor(() => expect(started).toEqual([models[0]!.id]), r);
    await waitFor(() => expect(need(r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('running'), r);
    const server = need(r, '[data-testid="local-server"]');
    expect(server.querySelector('[data-testid="local-server-state"]')!.getAttribute('aria-label')).toBe('running');
    // the mockup's address: host and port; the full base URL is its tooltip
    expect(server.querySelector('[data-testid="local-server-url"]')!.textContent).toBe('127.0.0.1:9933');
    expect(server.querySelector('[data-testid="local-server-url"]')!.getAttribute('title')).toBe('http://127.0.0.1:9933/v1');
    expect(need(r, '[data-testid="local-server-title"]').textContent).toBe('Serving gpt-oss-20b');
    expect(need(r, '[data-testid="local-server-title"]').getAttribute('title')).toBe(models[0]!.id);
    expect(need(r, '[data-testid="local-provider-note"]').textContent).toBe('Agents reach it as a provider: see Providers.');
    expect(need(r, '[data-testid="local-provider-note"] a').getAttribute('href')).toBe('/providers');
    // the served row says so, like the mockup's: Serving · GPU, downloaded; it cannot be served again, the others can
    expect(fit(models[0]!.id)!.textContent).toBe('Serving · GPU');
    expect(row(models[0]!.id).getAttribute('data-served')).toBe('true');
    expect(row(models[0]!.id).querySelector('.act')!.textContent).toBe('downloaded');
    expect(row(models[0]!.id).querySelector('[data-testid="local-serve"]')).toBe(null);
    expect((row(models[1]!.id).querySelector('[data-testid="local-serve"]') as HTMLButtonElement).disabled).toBe(false);
    // the log opens on demand
    expect(q(r, '[data-testid="local-log-lines"]')).toBe(null);
    await r.act(() => (need(r, '[data-testid="local-log"]') as HTMLButtonElement).click());
    expect(need(r, '[data-testid="local-log-lines"]').textContent).toContain('server is listening');

    // a server that runs is stopped with "Stop" (it says "Cancel start" only while a start waits)
    expect(need(r, '[data-testid="local-server-stop"]').textContent).toBe('Stop');
    expect((need(r, '[data-testid="local-server-stop"]') as HTMLButtonElement).disabled).toBe(false);
    await r.act(() => (need(r, '[data-testid="local-server-stop"]') as HTMLButtonElement).click());
    await waitFor(() => expect(need(r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('stopped'), r);
    expect(stops).toBe(1);
    expect(q(r, '[data-testid="local-stopped"]')).toBe(null); // nothing was waiting: no note beside the badge that says it
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
    expect(need(r, '[data-testid="local-server-state"]').getAttribute('aria-label')).toBe('starting');
    expect(need(r, '[data-testid="local-server-title"]').textContent).toBe('Starting other');
    await r.act(() => (row.querySelector('[data-testid="local-model-remove"]') as HTMLButtonElement).click());
    await r.act(() => (row.querySelector('[data-testid="local-model-remove-confirm"]') as HTMLButtonElement).click());
    await waitFor(() => expect(need(r, '[role="alert"]').textContent).toContain('stop the server first'), r);

    const failed = show({ localGet: async () => installed({ server: { state: 'failed', error: 'port 9933 is in use' } }) });
    await waitFor(() => expect(need(failed.r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('failed'), failed.r);
    expect(need(failed.r, '[data-testid="local-server"] [role="alert"]').textContent).toContain('port 9933 is in use');
  });

  // audit 2026-10-16, L44: a start waits for the core until the model is loaded (up to 15 minutes), and the core stops that wait
  // when the server is stopped (localllm.Server.Stop moves its generation on and kills the process, so Start returns "the server
  // was stopped before llama-server was ready"). Stop has to work meanwhile.
  describe('Stop while a start waits for the model', () => {
    const models = [model()];
    const id = models[0]!.id;
    const stopped = () => installed({ models });
    const runningView = () => installed({ models, server: { state: 'running', modelId: id, port: 9933, baseUrl: 'http://127.0.0.1:9933/v1' }, settings: { ...installed().settings, modelId: id, providerId: 'prov_local' } });
    const stopBtn = (r: ReturnType<typeof show>['r']) => need(r, '[data-testid="local-server-stop"]') as HTMLButtonElement;
    const serve = (r: ReturnType<typeof show>['r']) => r.act(() => (sel(r, '[data-testid="local-serve"]') as HTMLButtonElement).click());
    const START_STOPPED = 'the server was stopped before llama-server was ready';

    it('Stop is enabled and says Cancel start; it calls localServerStop while the start is still pending; the start\'s error then shows no alarm, only a note', async () => {
      const stops: string[] = [];
      let failStart: (e: Error) => void = () => undefined;
      let startedCalls = 0;
      const { r } = show({
        localGet: async () => stopped(),
        localServerStart: () => (
          startedCalls++,
          new Promise<LocalView>((_res, rej) => {
            failStart = rej;
          })
        ),
        localServerStop: async () => (stops.push('stop'), stopped()),
      });
      await waitFor(() => expect(r.$$('[data-testid="local-model"]').length).toBe(1), r);
      expect(q(r, '[data-testid="local-server-stop"]')).toBe(null); // nothing runs or starts yet
      await serve(r);
      await waitFor(() => expect(startedCalls).toBe(1), r);
      // the start waits on the core: Stop is there, enabled, and says it ends the start
      await waitFor(() => expect(stopBtn(r).textContent).toBe('Cancel start'), r);
      expect(stopBtn(r).disabled).toBe(false);
      expect(sel(r, '[data-testid="local-serve"]').textContent).toBe('Starting…');
      // every other control waits for the start
      for (const t of ['local-serve', 'local-model-remove', 'local-check-updates', 'local-suggested', 'local-context', 'local-port']) {
        expect((q(r, `[data-testid="${t}"]`) as HTMLButtonElement).disabled).toBe(true);
      }
      await r.act(() => stopBtn(r).click());
      await waitFor(() => expect(stops).toEqual(['stop']), r); // sent while the start has not answered
      expect(startedCalls).toBe(1);
      // the Stop answered first: a calm note that is announced, the server stopped, nothing left to stop; Serve still waits
      // for the start to deliver its answer
      await waitFor(() => expect(q(r, '[data-testid="local-stopped"]')?.textContent).toContain('The server was stopped.'), r);
      expect(q(r, '[data-testid="local-stopped"]')!.getAttribute('role')).toBe('status');
      expect(q(r, '[data-testid="local-server-stop"]')).toBe(null);
      expect(need(r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('stopped');
      expect((sel(r, '[data-testid="local-serve"]') as HTMLButtonElement).disabled).toBe(true);
      // the core ends the start with an error because it was stopped: no alarm
      failStart(new Error(START_STOPPED));
      await waitFor(() => expect((sel(r, '[data-testid="local-serve"]') as HTMLButtonElement).disabled).toBe(false), r);
      expect(r.$('[role="alert"]')).toBe(null);
      expect(q(r, '[data-testid="local-stopped"]')!.textContent).toContain('The server was stopped.');
      expect(sel(r, '[data-testid="local-serve"]').textContent).toBe('Serve');
    });

    it('also when the error of the start comes first, and the note goes when the next action starts', async () => {
      let failStart: (e: Error) => void = () => undefined;
      let finishStop: () => void = () => undefined;
      const { r } = show({
        localGet: async () => stopped(),
        localServerStart: () => new Promise<LocalView>((_res, rej) => (failStart = rej)),
        localServerStop: () => new Promise<LocalView>((res) => (finishStop = () => res(stopped()))),
      });
      await waitFor(() => expect(r.$$('[data-testid="local-model"]').length).toBe(1), r);
      await serve(r);
      await waitFor(() => expect(stopBtn(r).textContent).toBe('Cancel start'), r);
      await r.act(() => stopBtn(r).click());
      await waitFor(() => expect(stopBtn(r).textContent).toBe('Stopping…'), r);
      expect(stopBtn(r).disabled).toBe(true);
      failStart(new Error(START_STOPPED)); // the start ends first, the Stop is still on its way
      await drain();
      expect(r.$('[role="alert"]')).toBe(null);
      // nothing else can be pressed until the Stop has answered
      expect((sel(r, '[data-testid="local-serve"]') as HTMLButtonElement).disabled).toBe(true);
      finishStop();
      await waitFor(() => expect(q(r, '[data-testid="local-stopped"]')?.textContent).toContain('The server was stopped.'), r);
      expect(r.$('[role="alert"]')).toBe(null);
      // the next thing the person does clears the note
      await waitFor(() => expect((sel(r, '[data-testid="local-serve"]') as HTMLButtonElement).disabled).toBe(false), r);
      await serve(r);
      await waitFor(() => expect(q(r, '[data-testid="local-stopped"]')).toBe(null), r);
    });

    it('while the core itself says the server is starting (the autostart, another window) Stop says Cancel start too', async () => {
      const stops: string[] = [];
      const { r } = show({
        localGet: async () => installed({ models, server: { state: 'starting', modelId: id } }),
        localServerStop: async () => (stops.push('stop'), stopped()),
      });
      await waitFor(() => expect(stopBtn(r).textContent).toBe('Cancel start'), r);
      expect(stopBtn(r).disabled).toBe(false);
      // the model starting is the served row: it says so and offers no Serve; the menu waits for the start
      expect(q(r, '[data-testid="local-serve"]')).toBe(null);
      expect(need(r, '[data-testid="local-model"] [data-testid="local-fit"]').textContent).toBe('Starting · GPU');
      expect((need(r, '[data-testid="local-switch"]') as HTMLButtonElement).disabled).toBe(true);
      await r.act(() => stopBtn(r).click());
      await waitFor(() => expect(stops).toEqual(['stop']), r);
      await waitFor(() => expect(need(r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('stopped'), r);
      expect(q(r, '[data-testid="local-stopped"]')).toBe(null); // no start of this window was waiting
    });

    it('a start that fails by itself still shows its error: only a start the person cancelled is let go', async () => {
      const { r } = show({
        localGet: async () => stopped(),
        localServerStart: async () => {
          throw new Error('port 9933 is in use (another program listens on it; choose another port in the settings)');
        },
      });
      await waitFor(() => expect(r.$$('[data-testid="local-model"]').length).toBe(1), r);
      await serve(r);
      await waitFor(() => expect(need(r, '[role="alert"]').textContent).toContain('port 9933 is in use'), r);
      expect(q(r, '[data-testid="local-stopped"]')).toBe(null);
    });

    it('a Stop the core refuses shows why, and the start goes on: its answer counts', async () => {
      let finishStart: (v: LocalView) => void = () => undefined;
      const { r } = show({
        localGet: async () => stopped(),
        localServerStart: () => new Promise<LocalView>((res) => (finishStart = res)),
        localServerStop: async () => {
          throw new Error('only a person may stop the local model server');
        },
      });
      await waitFor(() => expect(r.$$('[data-testid="local-model"]').length).toBe(1), r);
      await serve(r);
      await waitFor(() => expect(stopBtn(r).textContent).toBe('Cancel start'), r);
      await r.act(() => stopBtn(r).click());
      await waitFor(() => expect(need(r, '[role="alert"]').textContent).toContain('only a person may stop the local model server'), r);
      expect(stopBtn(r).disabled).toBe(false); // it can be tried again while the start waits
      finishStart(runningView());
      await waitFor(() => expect(need(r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('running'), r);
      expect(stopBtn(r).textContent).toBe('Stop');
      expect(q(r, '[data-testid="local-stopped"]')).toBe(null);
    });

    it('a start that is ready just as Stop is pressed is let go too: the Stop\'s answer says what the server is', async () => {
      let finishStart: (v: LocalView) => void = () => undefined;
      const { r } = show({
        localGet: async () => stopped(),
        localServerStart: () => new Promise<LocalView>((res) => (finishStart = res)),
        localServerStop: async () => stopped(),
      });
      await waitFor(() => expect(r.$$('[data-testid="local-model"]').length).toBe(1), r);
      await serve(r);
      await waitFor(() => expect(stopBtn(r).textContent).toBe('Cancel start'), r);
      await r.act(() => stopBtn(r).click());
      await waitFor(() => expect(q(r, '[data-testid="local-stopped"]')).not.toBe(null), r);
      finishStart(runningView()); // the answer of a start the Stop outran
      await drain();
      await drain();
      expect(need(r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('stopped');
      expect(r.$('[role="alert"]')).toBe(null);
    });
  });

  // audit 2026-10-16, L40: a model is a large file that takes a long time to get again, so removing it asks first
  it('Remove of a downloaded model asks first: one click sends nothing, Keep cancels, Yes removes it once', async () => {
    const removed: string[] = [];
    const models = [model(), model({ id: 'x/y:Q4', variant: 'Q4', sizeBytes: 500 * MiB })];
    let state = installed({ models });
    const { r } = show({
      localGet: async () => state,
      localModelRemove: async (id) => {
        removed.push(id);
        state = installed({ models: models.filter((m) => m.id !== id) });
        return state;
      },
    });
    await waitFor(() => expect(r.$$('[data-testid="local-model"]').length).toBe(2), r);
    const row = (id: string) => need(r, `[data-testid="local-model"][data-id="${id}"]`);
    const first = models[0]!.id;
    await r.act(() => (row(first).querySelector('[data-testid="local-model-remove"]') as HTMLButtonElement).click());
    expect(removed).toEqual([]);
    expect(row(first).querySelector('[role="group"]')!.getAttribute('aria-label')).toBe(`Remove the model ${first}? 12.0 GB are deleted from this PC, and getting it back means downloading it all again.`);
    expect(row('x/y:Q4').querySelector('[data-testid="local-model-remove"]')).not.toBe(null); // only that model asks
    await r.act(() => (Array.from(row(first).querySelectorAll('button')).find((b) => b.getAttribute('aria-label') === `Keep the model ${first}`) as HTMLButtonElement).click());
    await waitFor(() => expect(row(first).querySelector('[data-testid="local-model-remove"]')).not.toBe(null), r);
    expect(removed).toEqual([]);
    await r.act(() => (row(first).querySelector('[data-testid="local-model-remove"]') as HTMLButtonElement).click());
    const yes = () => row(first).querySelector('[data-testid="local-model-remove-confirm"]') as HTMLButtonElement;
    expect(yes().getAttribute('aria-label')).toBe(`Yes, remove the model ${first}`);
    await r.act(() => {
      yes().click();
      yes().click();
    });
    await waitFor(() => expect(removed).toEqual([first]), r);
    await waitFor(() => expect(r.$$('[data-testid="local-model"]').length).toBe(1), r);
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
      'acme/tiny-GGUF': [
        { name: 'Q4_0', files: [], sizeBytes: 2 * MiB, fit: { target: 'gpu', fits: true, availableBytes: 14 * GiB } },
        { name: 'F16', files: [], sizeBytes: 80 * GiB, fit: { target: 'none', fits: false, availableBytes: 15 * GiB } },
      ],
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
    expect(variant.querySelector('.name')!.textContent).toBe('gpt-oss-20b · MXFP4');
    expect(variant.textContent).toContain('12.0 GB');
    expect(variant.querySelector('[data-testid="local-fit"]')!.textContent).toBe('Fits · GPU');
    expect(variant.textContent).toContain('the only quantization');
    expect(need(r, '[data-testid="local-repos-title"]').textContent).toBe('Suggested on Hugging Face');
    expect(first.getAttribute('aria-pressed')).toBe('true');

    // a search replaces the list; opening its repository closes the other
    type(need(r, '[data-testid="local-search-input"]') as HTMLInputElement, 'tiny');
    await submitSearch(r);
    await waitFor(() => expect(asked).toEqual(['tiny']), r);
    const tiny = await waitFor(() => need(r, '[data-testid="local-repo"][data-id="acme/tiny-GGUF"]'), r);
    expect(r.$$('[data-testid="local-repo"]').length).toBe(1);
    expect(need(r, '[data-testid="local-repos"]').getAttribute('aria-label')).toBe('Search results');
    expect(need(r, '[data-testid="local-repos-title"]').textContent).toBe('Hugging Face, "tiny": 1 repository');
    await r.act(() => tiny.click());
    const q4 = await waitFor(() => need(r, '[data-testid="local-variant"][data-name="Q4_0"]'), r);
    expect(r.$$('[data-testid="local-variant"]').length).toBe(2);
    // a variant too big for this PC is "Too big for 15 GB" and not offered, as in the mockup (decided by the fit the core sent,
    // not by the badge's words); its words say why
    const f16 = need(r, '[data-testid="local-variant"][data-name="F16"]');
    expect(f16.querySelector('[data-testid="local-fit"]')!.textContent).toBe('Too big for 15 GB');
    expect(f16.querySelector('[data-testid="local-fit"]')!.getAttribute('title')).toBe('Too big for this PC (needs about 80.0 GB, 15.0 GB free): Drydock does not download a model it could not load.');
    expect(f16.querySelector('[data-testid="local-download"]')).toBe(null);
    expect(f16.querySelector('[data-testid="local-not-offered"]')!.textContent).toBe('not offered');
    expect(f16.querySelector('[data-testid="local-not-offered"]')!.getAttribute('title')).toContain('Drydock does not download a model it could not load');
    expect((q4.querySelector('[data-testid="local-download"]') as HTMLButtonElement).disabled).toBe(false);
    await r.act(() => (q4.querySelector('[data-testid="local-download"]') as HTMLButtonElement).click());
    await waitFor(() => expect(downloads).toEqual([['acme/tiny-GGUF', 'Q4_0']]), r);
    await waitFor(() => need(r, '[data-testid="local-download-row"][data-id="acme/tiny-GGUF:Q4_0"]'), r);
    // the variant being downloaded is not offered twice
    expect(need(r, '[data-testid="local-variant"][data-name="Q4_0"] .act').textContent).toBe('downloading');

    // the suggestions come back in place of the search results
    const again = await enabled<HTMLButtonElement>(r, '[data-testid="local-suggested"]');
    await r.act(() => again.click());
    await waitFor(() => expect(r.$$('[data-testid="local-repo"]').length).toBe(2), r);
    expect(need(r, '[data-testid="local-repos"]').getAttribute('aria-label')).toBe('Suggested models');
    expect(q(r, '[data-testid="local-repo"][data-id="acme/tiny-GGUF"]')).toBe(null);
  });

  it('Close suggestions shrinks the page back: the list and the open repository go, Show suggestions returns; Close results the same', async () => {
    let suggestions = 0;
    const { r } = show({
      localGet: async () => installed(),
      localModelsSuggested: async () => (suggestions++, [{ id: 'ggml-org/gpt-oss-20b-GGUF', downloads: 900000, likes: 10, updated: '', tags: [] }]),
      localModelsSearch: async () => [{ id: 'acme/tiny-GGUF', downloads: 10, likes: 0, updated: '', tags: [] }],
      localModelVariants: async () => [{ name: 'MXFP4', files: [], sizeBytes: 12 * GiB, fit: { target: 'gpu', fits: true, availableBytes: 14 * GiB } }],
    });
    await waitFor(() => need(r, '[data-testid="local-suggested"]'), r);
    expect(q(r, '[data-testid="local-repos-close"]')).toBe(null);
    await r.act(() => (need(r, '[data-testid="local-suggested"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$$('[data-testid="local-repo"]').length).toBe(1), r);
    // while the suggestions are shown, Show suggestions gives way to Close suggestions
    expect(q(r, '[data-testid="local-suggested"]')).toBe(null);
    expect(need(r, '[data-testid="local-repos-close"]').textContent).toBe('Close suggestions');
    await r.act(() => need(r, '[data-testid="local-repo"]').click());
    await waitFor(() => need(r, '[data-testid="local-variant"]'), r);

    await r.act(() => (need(r, '[data-testid="local-repos-close"]') as HTMLButtonElement).click());
    await waitFor(() => expect(q(r, '[data-testid="local-repos"]')).toBe(null), r);
    expect(q(r, '[data-testid="local-variant"]')).toBe(null);
    expect(q(r, '[data-testid="local-repos-close"]')).toBe(null);
    expect(need(r, '[data-testid="local-suggested"]').textContent).toBe('Show suggestions');
    // opening them again asks the core again (nothing stale is kept), and the repository opened before is closed
    await r.act(() => (need(r, '[data-testid="local-suggested"]') as HTMLButtonElement).click());
    await waitFor(() => expect(r.$$('[data-testid="local-repo"]').length).toBe(1), r);
    expect(suggestions).toBe(2);
    expect(need(r, '[data-testid="local-repo"]').getAttribute('aria-pressed')).toBe('false');

    // search results: both Show suggestions and Close results are offered; Close results clears the list, the words typed stay
    type(need(r, '[data-testid="local-search-input"]') as HTMLInputElement, 'tiny');
    await submitSearch(r);
    await waitFor(() => need(r, '[data-testid="local-repo"][data-id="acme/tiny-GGUF"]'), r);
    expect(need(r, '[data-testid="local-suggested"]').textContent).toBe('Show suggestions');
    expect(need(r, '[data-testid="local-repos-close"]').textContent).toBe('Close results');
    await r.act(() => (need(r, '[data-testid="local-repos-close"]') as HTMLButtonElement).click());
    await waitFor(() => expect(q(r, '[data-testid="local-repos"]')).toBe(null), r);
    expect(q(r, '[data-testid="local-repos-close"]')).toBe(null);
    expect((need(r, '[data-testid="local-search-input"]') as HTMLInputElement).value).toBe('tiny');
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
    expect(optionsOf(cache).map((o) => o.value)).toEqual(['q8_0', 'q4_0', 'f16']);
    expect(valueOf(cache)).toBe('q8_0');
    expect(need(r, '[data-testid="local-settings"]').textContent).toContain('q8_0: near-lossless, half the KV memory of f16');
    expect(need(r, '[data-testid="local-settings"] h2').textContent).toBe('Server settings');
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
    expect(optionsOf(sel(r, '[data-testid="local-cache"]')).map((o) => o.value)).toEqual(['turbo4', 'turbo3', 'q8_0', 'q4_0', 'f16']);
    expect(valueOf(sel(r, '[data-testid="local-cache"]'))).toBe('turbo4');
  });

  // decision P77: the engine comes installed with Drydock, with Google's TurboQuant built in. Decision P95: "Check for update
  // should then install the update": the check installs a newer build itself, and a newer build found by the twice-daily check
  // turns the button into Update to, which does the same.
  it('the build that came with Drydock says so and names the TurboQuant cache types; Check for updates installs a newer build', async () => {
    const bundled = (over: Partial<LocalView['runtime']> = {}) =>
      installed({
        runtime: { ...installed().runtime, source: 'turboquant', sourceName: FORK.name, tag: 'b10298-2.0.0', bundled: true, ...over },
        settings: { ...installed().settings, source: 'turboquant', cacheType: 'turbo4' },
      });
    let next = bundled();
    let state = bundled();
    const calls: unknown[] = [];
    const { r, s } = show({
      localGet: async () => state,
      localCheckUpdates: async () => (calls.push('check'), next),
      localInstall: async (...a) => (calls.push(a), next),
    });
    await waitFor(() => need(r, '[data-testid="local-runtime"][data-installed="true"]'), r);
    expect(need(r, '[data-testid="local-bundled"]').textContent).toBe('came with Drydock');
    expect(need(r, '[data-testid="local-build"]').textContent).toBe('TurboQuant build b10298-2.0.0 · Vulkan');
    expect(need(r, '[data-testid="local-build"] span').textContent).toBe('b10298-2.0.0'); // the tag in the mono face
    expect(need(r, '[data-testid="local-turboquant"]').textContent).toBe("Google's TurboQuant is built in: turbo4 and turbo3 keep more context in less memory.");
    expect(q(r, '[data-testid="local-up-to-date"]')).toBe(null);
    // nothing newer: said once the check is back
    await r.act(() => (need(r, '[data-testid="local-check-updates"]') as HTMLButtonElement).click());
    await waitFor(() => expect(need(r, '[data-testid="local-up-to-date"]').textContent).toBe('This is the newest build.'), r);
    // a newer build: the check's answer is its install running, shown at once
    next = bundled({ job: { kind: 'install', target: 'TurboQuant build for Vulkan', state: 'running', phase: 'downloading b10350-2.1.0', done: 0, total: 100 * MiB, started: '' } });
    await r.act(async () => (await enabled(r, '[data-testid="local-check-updates"]')).click());
    await waitFor(() => expect(need(r, '[data-testid="local-job"]').textContent).toContain('downloading b10350-2.1.0'), r);
    expect(calls).toEqual(['check', 'check']);
    // the twice-daily check found a newer build: the button names it and asks the core to check and install, as above
    state = bundled({ latest: 'b10400-2.2.0' });
    next = bundled({ latest: 'b10400-2.2.0', job: { kind: 'install', target: 'TurboQuant build for Vulkan', state: 'running', phase: 'downloading b10400-2.2.0', done: 0, total: 100 * MiB, started: '' } });
    s.emit({ type: 'local.changed', data: { what: 'runtime' } });
    await waitFor(() => expect(need(r, '[data-testid="local-check-updates"]').textContent).toBe('Update to b10400-2.2.0'), r);
    await r.act(async () => (await enabled(r, '[data-testid="local-check-updates"]')).click());
    await waitFor(() => expect(calls).toEqual(['check', 'check', 'check']), r);
  });

  it('a build the person installed (not the one that came with Drydock) has no came-with badge, and the official build has no TurboQuant note', async () => {
    const { r } = show({ localGet: async () => installed() });
    await waitFor(() => need(r, '[data-testid="local-runtime"][data-installed="true"]'), r);
    expect(q(r, '[data-testid="local-bundled"]')).toBe(null);
    expect(q(r, '[data-testid="local-turboquant"]')).toBe(null);
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

  it("the page's head: the title, the privacy line, the Hub search; \"/\" goes to the search from anywhere but a field", async () => {
    const { r } = show({ localGet: async () => installed() });
    await waitFor(() => need(r, '[data-testid="local-search-input"]'), r);
    expect(need(r, 'h1').textContent).toBe('Models on this PC');
    expect(need(r, '[data-testid="local-models"]').textContent).toContain('Private: prompts and code never leave this computer');
    const input = need(r, '[data-testid="local-search-input"]') as HTMLInputElement;
    expect(input.getAttribute('aria-label')).toBe('Find a model');
    expect(input.placeholder).toBe('Find a model on Hugging Face');
    expect(need(r, '[data-testid="local-search"] kbd').textContent).toBe('/');
    expect(document.activeElement).not.toBe(input);
    const slash = (target: EventTarget, init: KeyboardEventInit = {}) => target.dispatchEvent(new KeyboardEvent('keydown', { key: '/', bubbles: true, cancelable: true, ...init }));
    // with Ctrl it is not the shortcut
    await r.act(() => void slash(document.body, { ctrlKey: true }));
    expect(document.activeElement).not.toBe(input);
    // in another field it is a character typed
    const other = need(r, '[data-testid="local-context"]') as HTMLInputElement;
    other.focus();
    let delivered = true;
    await r.act(() => void (delivered = slash(other)));
    expect(delivered).toBe(true);
    expect(document.activeElement).toBe(other);
    other.blur();
    await r.act(() => void (delivered = slash(document.body)));
    expect(delivered).toBe(false); // taken by the page
    expect(document.activeElement).toBe(input);
  });

  it('the Serving card: the model, its address, the agents that use it, the KV cache and context, when it started; Memory splits the GPU into the model and its KV cache', async () => {
    const m = model();
    const kv = { known: true, bytes: 2 * GiB, contextSize: 32768, cacheType: 'turbo4' };
    const { r } = show({
      localGet: async () =>
        installed({
          models: [m],
          server: { state: 'running', modelId: m.id, port: 9933, baseUrl: 'http://127.0.0.1:9933/v1', startedAt: '2026-10-22T09:00:00Z', autostarted: true, cacheType: 'turbo4', contextSize: 32768 },
          settings: { ...installed().settings, modelId: m.id, providerId: 'prov_local' },
          usedBy: [
            { agentId: 'a1', name: 'Local helper', projectId: 'p1', projectName: 'gocalc' },
            { agentId: 'a2', name: 'Reviewer', projectId: 'p1', projectName: 'gocalc' },
          ],
          served: { modelId: m.id, modelBytes: 10 * GiB, kvBytes: 2 * GiB, kv, device: 'gpu', deviceName: 'NVIDIA GeForce RTX 4060 Ti', deviceTotal: 16 * GiB, deviceFree: 14 * GiB, fit: { target: 'gpu', fits: true, availableBytes: 14 * GiB } },
        }),
    });
    await waitFor(() => expect(need(r, '[data-testid="local-server"]').getAttribute('data-state')).toBe('running'), r);
    const card = need(r, '[data-testid="local-server"]');
    expect(need(r, '[data-testid="local-server-title"]').textContent).toBe('Serving gpt-oss-20b');
    expect(need(r, '[data-testid="local-server-url"]').textContent).toBe('127.0.0.1:9933');
    expect(Array.from(card.querySelectorAll('dt')).map((d) => d.textContent)).toEqual(['Used by', 'KV cache', 'Started']);
    expect(need(r, '[data-testid="local-used-by"]').textContent).toBe('Local helper, Reviewer');
    expect(need(r, '[data-testid="local-kv"]').textContent).toBe('turbo4 · 32k context');
    expect(need(r, '[data-testid="local-started"]').textContent).toBe('at login, 3 h ago');
    expect(need(r, '[data-testid="local-server-stop"]').textContent).toBe('Stop');
    expect(need(r, '[data-testid="local-switch"]').textContent).toBe('Switch model');
    // Memory: 2 GB in use before the model, then the model (10 GB) and its KV cache (2 GB): 14 of 16 GB
    const gpu = need(r, '[data-testid="local-device"][data-id="CUDA0"]');
    expect(gpu.querySelector('[data-testid="local-bar-used"]')!.textContent).toBe('14 of 16 GB');
    expect(Array.from(gpu.querySelectorAll('[data-part]')).map((p) => [p.getAttribute('data-part'), (p as HTMLElement).style.width])).toEqual([
      ['model', '62.5%'],
      ['kv', '12.5%'],
      ['other', '12.5%'],
    ]);
    expect(gpu.querySelector('.legend')!.textContent).toBe('modelKV cache');
    expect(gpu.querySelector('[role="img"]')!.getAttribute('aria-label')).toBe('GPU · CUDA: 14 of 16 GB used: model 10.0 GB, KV cache 2.0 GB, other programs 2.0 GB');
    expect(q(r, '[data-testid="local-ram"] .legend')).toBe(null);
  });

  it('a KV cache the core could not estimate is said in the legend, its reason as the tooltip; a model served in RAM is split in the RAM bar', async () => {
    const m = model({ fit: { target: 'cpu', fits: true, availableBytes: 15 * GiB } });
    const kv = { known: false, contextSize: 0, cacheType: 'turbo4', note: 'the TurboQuant types are not estimated' };
    const { r } = show({
      localGet: async () =>
        installed({
          runtime: { ...installed().runtime, devices: { probed: true, devices: [] } },
          models: [m],
          server: { state: 'running', modelId: m.id, port: 9933, baseUrl: 'http://127.0.0.1:9933/v1' },
          served: { modelId: m.id, modelBytes: 8 * GiB, kvBytes: 0, kv, device: 'ram', deviceTotal: 32 * GiB, deviceFree: 20 * GiB, fit: { target: 'cpu', fits: true, availableBytes: 15 * GiB } },
        }),
    });
    const ram = await waitFor(() => need(r, '[data-testid="local-ram"]'), r);
    expect(need(r, '[data-testid="local-devices"]').textContent).toBe('No GPU found: runs on the CPU');
    expect(Array.from(ram.querySelectorAll('[data-part]')).map((p) => p.getAttribute('data-part'))).toEqual(['model', 'other']);
    const note = Array.from(ram.querySelectorAll('.legend > span'))[1] as HTMLElement;
    expect(note.textContent).toBe('KV cache not estimated');
    expect(note.getAttribute('title')).toBe('the TurboQuant types are not estimated');
    // the served row: no GPU, so RAM
    expect(need(r, '[data-testid="local-model"] [data-testid="local-fit"]').textContent).toBe('Serving · RAM');
    // no start time known: no Started line
    expect(q(r, '[data-testid="local-started"]')).toBe(null);
  });

  it('Switch model serves another downloaded model; stopped, the same menu is Serve a model; one model alone has nothing to switch to', async () => {
    const a = model();
    const b = model({ id: 'acme/tiny-GGUF:Q4_0', repo: 'acme/tiny-GGUF', variant: 'Q4_0', sizeBytes: 2 * MiB });
    const started: string[] = [];
    const running = (id: string, models = [a, b]) => installed({ models, server: { state: 'running', modelId: id, port: 9933, baseUrl: 'http://127.0.0.1:9933/v1' } });
    let state = running(a.id);
    const { r } = show({
      localGet: async () => state,
      localServerStart: async (id) => (started.push(id), (state = running(id))),
    });
    const trigger = await waitFor(() => need(r, '[data-testid="local-switch"]') as HTMLButtonElement, r);
    expect(trigger.disabled).toBe(false);
    await openMenu(r, trigger);
    const items = Array.from(r.$$('[role="menu"][aria-label="Switch model"] [role="menuitemradio"]'));
    expect(items.map((i) => [i.textContent!.trim(), i.getAttribute('aria-checked')])).toEqual([
      ['gpt-oss-20b · MXFP4', 'true'],
      ['tiny · Q4_0', 'false'],
    ]);
    await r.act(() => (r.$(`[role="menuitemradio"][data-value="${b.id}"]`) as HTMLElement).click());
    await waitFor(() => expect(started).toEqual([b.id]), r);
    await waitFor(() => expect(need(r, '[data-testid="local-server-title"]').textContent).toBe('Serving tiny'), r);

    // stopped: Serve a model, the same list
    state = installed({ models: [a, b] });
    cleanup();
    const stopped = show({ localGet: async () => state, localServerStart: async (id) => (started.push(id), (state = running(id))) });
    const serveMenu = await waitFor(() => need(stopped.r, '[data-testid="local-switch"]') as HTMLButtonElement, stopped.r);
    expect(serveMenu.textContent).toBe('Serve a model');
    await openMenu(stopped.r, serveMenu);
    await stopped.r.act(() => (stopped.r.$(`[role="menu"][aria-label="Serve a model"] [role="menuitemradio"][data-value="${a.id}"]`) as HTMLElement).click());
    await waitFor(() => expect(started).toEqual([b.id, a.id]), stopped.r);

    // one model, served: nothing to switch to; no model at all: nothing to serve
    cleanup();
    const one = show({ localGet: async () => running(a.id, [a]) });
    const lone = await waitFor(() => need(one.r, '[data-testid="local-switch"]') as HTMLButtonElement, one.r);
    expect(lone.disabled).toBe(true);
    expect(lone.title).toBe('Download another model to switch to');
    cleanup();
    const none = show({ localGet: async () => installed() });
    const empty = await waitFor(() => need(none.r, '[data-testid="local-switch"]') as HTMLButtonElement, none.r);
    expect(empty.disabled).toBe(true);
    expect(empty.title).toBe('Download a model first');
    expect(need(none.r, '[data-testid="local-models-card"] .empty').textContent).toBe('No model yet: find one on Hugging Face.');
  });
});
