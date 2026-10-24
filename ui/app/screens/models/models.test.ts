import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { LocalJob, LocalModel, LocalView } from '../../api/localClient.ts';
import {
  barLabel,
  compactCount,
  contextWords,
  deviceLine,
  diskLine,
  fitWords,
  gb,
  gbNumber,
  gpus,
  hostPort,
  jobFraction,
  localRow,
  memoryBar,
  memoryBars,
  modelName,
  progressText,
  runtimeLine,
  servedName,
  serverTone,
  splitModelId,
  startedWords,
  switchChoices,
  usedByWords,
  usedOf,
  wholeNumber,
} from './models.ts';

const GiB = 2 ** 30;
const MiB = 2 ** 20;
const job = (over: Partial<LocalJob> = {}): LocalJob => ({ kind: 'install', target: 'x', state: 'running', done: 0, total: 0, started: '', ...over });

describe('Models on this PC helpers', () => {
  it('sizes: GB with one decimal, whole MB under a gigabyte, never "0.0 GB"; the bars drop a ".0"', () => {
    expect(gb(8.5 * GiB)).toBe('8.5 GB');
    expect(gb(120 * GiB)).toBe('120.0 GB');
    expect(gb(24 * MiB)).toBe('24 MB');
    expect(gb(2 * MiB)).toBe('2 MB');
    expect(gb(0)).toBe('0 MB');
    expect(gb(undefined)).toBe('0 MB');
    expect(gbNumber(16 * GiB)).toBe('16');
    expect(gbNumber(12.4 * GiB)).toBe('12.4');
    expect(gbNumber(-1)).toBe('0');
    expect(usedOf(12.4 * GiB, 16 * GiB)).toBe('12.4 of 16 GB');
    // more used than there is (a stale reading) is drawn as all of it
    expect(usedOf(20 * GiB, 16 * GiB)).toBe('16 of 16 GB');
  });

  it("fit badges in the mockup's words: Fits · GPU, Fits · GPU + RAM, Too big for N GB; Serving for the served one; unknown shows nothing", () => {
    const gpu = { target: 'gpu', fits: true, availableBytes: 14 * GiB } as const;
    expect(fitWords(gpu, 12 * GiB, { gpu: true })).toEqual({ text: 'Fits · GPU', long: 'Runs on the GPU (needs about 12.0 GB, 14.0 GB free).', tone: 'ok' });
    expect(fitWords(gpu, 12 * GiB, { gpu: true, serving: true })!.text).toBe('Serving · GPU');
    const cpu = { target: 'cpu', fits: true, availableBytes: 15 * GiB } as const;
    expect(fitWords(cpu, 18.6 * GiB, { gpu: true })).toEqual({
      text: 'Fits · GPU + RAM',
      long: 'Runs on the GPU and in RAM (slower): no GPU has room for all of it (needs about 18.6 GB, 15.0 GB free).',
      tone: 'warn',
    });
    expect(fitWords(cpu, 18.6 * GiB, { gpu: false })!.text).toBe('Fits · RAM');
    expect(fitWords(cpu, 18.6 * GiB, { gpu: true, serving: true })!.text).toBe('Serving · GPU + RAM');
    const none = fitWords({ target: 'none', fits: false, availableBytes: 48 * GiB }, 63.4 * GiB, { gpu: true })!;
    expect(none.text).toBe('Too big for 48 GB');
    expect(none.tone).toBe('err');
    expect(none.long).toBe('Too big for this PC (needs about 63.4 GB, 48.0 GB free): Drydock does not download a model it could not load.');
    expect(fitWords({ target: 'unknown', fits: true, availableBytes: 0 }, 1, { gpu: true })).toBe(undefined);
    expect(fitWords(undefined, 1, { gpu: true })).toBe(undefined);
    expect(fitWords(undefined, 1, { gpu: true, serving: true })!.text).toBe('Serving');
    // the KV cache the core estimated counts: the need is the model with it
    expect(fitWords({ ...gpu, kvBytes: 2 * GiB, needBytes: 14 * GiB }, 12 * GiB, { gpu: true })!.long).toBe('Runs on the GPU (needs about 14.0 GB with its KV cache, 14.0 GB free).');
    // half the numbers is no number: the plain sentence
    expect(fitWords({ target: 'gpu', fits: true, availableBytes: 0 }, 2 * MiB, { gpu: true })!.long).toBe('Runs on the GPU.');
  });

  it('names: the model from its repository, the id split into repository and variant', () => {
    expect(modelName('ggml-org/gpt-oss-20b-GGUF')).toBe('gpt-oss-20b');
    expect(modelName('unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF')).toBe('Qwen3-Coder-30B-A3B-Instruct');
    expect(modelName('acme/tiny_gguf')).toBe('tiny');
    expect(modelName('acme/plain')).toBe('plain');
    expect(modelName('GGUF')).toBe('GGUF');
    expect(splitModelId('acme/tiny-GGUF:Q4_0')).toEqual({ repo: 'acme/tiny-GGUF', variant: 'Q4_0' });
    expect(splitModelId('acme/tiny-GGUF')).toEqual({ repo: 'acme/tiny-GGUF', variant: '' });
  });

  it("the Serving card's lines: the address, the context, who uses it, when it started", () => {
    expect(hostPort('http://127.0.0.1:9933/v1')).toBe('127.0.0.1:9933');
    expect(hostPort(undefined, 9944)).toBe('127.0.0.1:9944');
    expect(hostPort('not a url')).toBe('not a url');
    expect(hostPort(undefined)).toBe('');
    expect(contextWords(32768)).toBe('32k context');
    expect(contextWords(40000)).toBe('40000 context');
    expect(contextWords(0)).toBe("the model's own context");
    expect(usedByWords([])).toBe('No agent yet');
    expect(usedByWords([{ agentId: 'a', name: 'Ada', projectId: 'p', projectName: 'gocalc' }, { agentId: 'b', name: 'Bob', projectId: 'p', projectName: 'gocalc' }])).toBe('Ada, Bob');
    expect(usedByWords([{ agentId: 'a', name: 'Ada', projectId: 'p', projectName: 'gocalc' }, { agentId: 'b', name: 'Bob', projectId: 'q', projectName: 'web' }])).toBe('Ada (gocalc), Bob (web)');
    const now = Date.parse('2026-10-22T12:00:00Z');
    expect(startedWords('2026-10-22T09:00:00Z', true, now)).toBe('at login, 3 h ago');
    expect(startedWords('2026-10-22T11:46:00Z', false, now)).toBe('14 min ago');
    expect(startedWords(undefined, true, now)).toBe('at login');
    expect(startedWords('0001-01-01T00:00:00Z', false, now)).toBe('');
  });

  it('a memory bar: the model and its KV cache first, the rest after, never past the total', () => {
    expect(memoryBar(16 * GiB, 0.5 * GiB, 10 * GiB, 2 * GiB)).toEqual({
      total: 16 * GiB,
      used: 12.5 * GiB,
      parts: [
        { kind: 'model', bytes: 10 * GiB },
        { kind: 'kv', bytes: 2 * GiB },
        { kind: 'other', bytes: 0.5 * GiB },
      ],
    });
    expect(memoryBar(16 * GiB, 10 * GiB, 10 * GiB, 0).parts).toEqual([
      { kind: 'model', bytes: 10 * GiB },
      { kind: 'other', bytes: 6 * GiB },
    ]);
    expect(memoryBar(16 * GiB, 0).parts).toEqual([]);
    expect(barLabel('GPU · CUDA', memoryBar(16 * GiB, 0.5 * GiB, 10 * GiB, 2 * GiB))).toBe('GPU · CUDA: 12.5 of 16 GB used: model 10.0 GB, KV cache 2.0 GB, other programs 512 MB');
  });

  it('the memory bars of a view: the served model in the GPU it is measured against, else in the RAM', () => {
    const base = {
      runtime: {
        installed: true,
        devices: {
          probed: true,
          devices: [
            { id: 'CPU', kind: 'CPU', name: 'cpu', totalBytes: 64 * GiB, freeBytes: 60 * GiB },
            { id: 'CUDA0', kind: 'CUDA', name: 'RTX 4060 Ti', totalBytes: 16 * GiB, freeBytes: 14 * GiB },
          ],
        },
      },
      memory: { known: true, totalBytes: 32 * GiB, availableBytes: 20 * GiB },
    } as unknown as LocalView;
    const idle = memoryBars(base);
    expect(idle.gpus.map((g) => [g.device.id, g.serving, g.bar.used])).toEqual([['CUDA0', false, 2 * GiB]]);
    expect(idle.ram).toEqual({ total: 32 * GiB, used: 12 * GiB, parts: [{ kind: 'other', bytes: 12 * GiB }] });
    const kv = { known: true, bytes: 2 * GiB, contextSize: 32768, cacheType: 'turbo4' };
    const onGpu = memoryBars({ ...base, served: { modelId: 'm', modelBytes: 10 * GiB, kvBytes: 2 * GiB, kv, device: 'gpu', deviceName: 'RTX 4060 Ti', deviceTotal: 16 * GiB, deviceFree: 14 * GiB, fit: { target: 'gpu', fits: true, availableBytes: 14 * GiB } } });
    expect(onGpu.gpus[0]!.serving).toBe(true);
    expect(onGpu.gpus[0]!.bar.parts.map((p) => p.kind)).toEqual(['model', 'kv', 'other']);
    expect(onGpu.gpus[0]!.bar.used).toBe(14 * GiB);
    expect(onGpu.ram!.parts.map((p) => p.kind)).toEqual(['other']);
    const inRam = memoryBars({ ...base, served: { modelId: 'm', modelBytes: 8 * GiB, kvBytes: 1 * GiB, kv, device: 'ram', deviceTotal: 32 * GiB, deviceFree: 20 * GiB, fit: { target: 'cpu', fits: true, availableBytes: 15 * GiB } } });
    expect(inRam.gpus[0]!.serving).toBe(false);
    expect(inRam.ram!.parts).toEqual([
      { kind: 'model', bytes: 8 * GiB },
      { kind: 'kv', bytes: 1 * GiB },
      { kind: 'other', bytes: 3 * GiB },
    ]);
    // no RAM reading: no RAM bar
    expect(memoryBars({ ...base, memory: { known: false, totalBytes: 0, availableBytes: 0 } }).ram).toBe(undefined);
  });

  it('server tones', () => {
    expect(serverTone('running')).toBe('ok');
    expect(serverTone('starting')).toBe('info');
    expect(serverTone('failed')).toBe('err');
    expect(serverTone('stopped')).toBe('muted');
  });

  it('job progress: a fraction when the size is known, the phase otherwise', () => {
    expect(jobFraction(job({ done: 12 * MiB, total: 24 * MiB }))).toBe(0.5);
    expect(jobFraction(job({ done: 30, total: 24 }))).toBe(1);
    expect(jobFraction(job())).toBe(undefined);
    expect(jobFraction(undefined)).toBe(undefined);
    expect(progressText(job({ done: 12 * MiB, total: 24 * MiB, phase: 'downloading b11669' }))).toBe('12 MB of 24 MB (50%), downloading b11669');
    expect(progressText(job({ phase: 'looking up the newest build' }))).toBe('looking up the newest build');
    expect(progressText(job())).toBe('working');
  });

  it('counts and lines', () => {
    expect(compactCount(900000)).toBe('900k');
    expect(compactCount(1_250_000)).toBe('1.3M');
    expect(compactCount(10)).toBe('10');
    expect(compactCount(0)).toBe('0');
    const label = (id: string) => (id === 'vulkan' ? 'Vulkan' : id);
    expect(runtimeLine({ installed: true, tag: 'b11669', backend: 'vulkan', sourceName: 'Official weekly build', devices: { probed: false, devices: [] } }, label)).toBe(
      'Official weekly build b11669 · Vulkan',
    );
    expect(deviceLine({ id: 'CUDA0', kind: 'CUDA', name: 'NVIDIA GeForce RTX 4060 Ti', totalBytes: 16 * GiB, freeBytes: 14 * GiB })).toBe('CUDA0: NVIDIA GeForce RTX 4060 Ti, 14.0 GB free of 16.0 GB');
    expect(gpus([{ id: 'CPU', kind: 'CPU', name: 'cpu', totalBytes: 0, freeBytes: 0 }, { id: 'Vulkan0', kind: 'Vulkan', name: 'g', totalBytes: 1, freeBytes: 1 }]).map((d) => d.id)).toEqual(['Vulkan0']);
  });

  it('the disk line leaves out what is not known', () => {
    const v = { modelsBytes: 26 * MiB, freeBytes: 120 * GiB } as LocalView;
    expect(diskLine(v)).toBe('Models on disk: 26 MB · Free disk: 120.0 GB');
    expect(diskLine({ ...v, freeBytes: 0 })).toBe('Models on disk: 26 MB');
  });

  it('Switch model offers the downloaded models, the served one checked', () => {
    const m = (id: string, repo: string, variant: string) => ({ id, repo, variant }) as LocalModel;
    expect(switchChoices([m('a/x-GGUF:Q4_0', 'a/x-GGUF', 'Q4_0'), m('b/y-GGUF:MXFP4', 'b/y-GGUF', 'MXFP4')], 'b/y-GGUF:MXFP4')).toEqual([
      { id: 'a/x-GGUF:Q4_0', label: 'x · Q4_0', current: false },
      { id: 'b/y-GGUF:MXFP4', label: 'y · MXFP4', current: true },
    ]);
  });

  it('whole numbers from a settings box', () => {
    expect(wholeNumber('65536')).toBe(65536);
    expect(wholeNumber(' 0 ')).toBe(0);
    expect(wholeNumber('')).toBe(undefined);
    expect(wholeNumber('12.5')).toBe(undefined);
    expect(wholeNumber('-1')).toBe(undefined);
    expect(wholeNumber('abc')).toBe(undefined);
  });

  it('the Models page row: the state in words and the model served, else whether the engine is set up', () => {
    const rt = (over: Partial<LocalView['runtime']> = {}): LocalView['runtime'] => ({ installed: true, devices: { probed: false, devices: [] }, ...over });
    expect(servedName('ggml-org/gpt-oss-20b-GGUF:MXFP4')).toBe('gpt-oss-20b · MXFP4');
    expect(servedName('plain')).toBe('plain');
    expect(localRow({ supported: false, runtime: rt(), server: { state: 'stopped' } })).toEqual({ text: 'Not available', tone: 'muted', sub: 'Not set up' });
    expect(localRow({ supported: true, runtime: rt({ installed: false }), server: { state: 'stopped' } })).toEqual({ text: 'Set up', tone: 'none', sub: 'Not set up' });
    const job: LocalJob = { kind: 'install', target: 'x', state: 'running', done: 0, total: 0, started: '' };
    expect(localRow({ supported: true, runtime: rt({ installed: false, job }), server: { state: 'stopped' } })).toEqual({ text: 'Setting up', tone: 'info', sub: 'Not set up' });
    expect(localRow({ supported: true, runtime: rt(), server: { state: 'stopped' } })).toEqual({ text: 'Stopped', tone: 'muted', sub: 'Nothing served' });
    expect(localRow({ supported: true, runtime: rt(), server: { state: 'running', modelId: 'acme/tiny-GGUF:Q4_0' } })).toEqual({ text: 'Serving', tone: 'ok', sub: 'tiny · Q4_0' });
    expect(localRow({ supported: true, runtime: rt(), server: { state: 'starting', modelId: 'acme/tiny-GGUF:Q4_0' } })).toEqual({ text: 'Starting', tone: 'info', sub: 'tiny · Q4_0' });
    expect(localRow({ supported: true, runtime: rt(), server: { state: 'failed' } })).toEqual({ text: 'Failed', tone: 'err', sub: 'Nothing served' });
    // a view of a PC that cannot run local models may carry nothing else
    expect(localRow({ supported: false } as LocalView).text).toBe('Not available');
  });
});
