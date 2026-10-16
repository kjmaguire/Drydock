import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import type { LocalJob, LocalView } from '../../api/localClient.ts';
import { compactCount, deviceLine, fitText, gb, gpus, jobFraction, memoryLine, progressText, runtimeLine, serverTone, wholeNumber } from './local-models.ts';

const GiB = 2 ** 30;
const MiB = 2 ** 20;
const job = (over: Partial<LocalJob> = {}): LocalJob => ({ kind: 'install', target: 'x', state: 'running', done: 0, total: 0, started: '', ...over });

describe('local models helpers', () => {
  it('sizes: GB with one decimal, whole MB under a gigabyte, never "0.0 GB"', () => {
    expect(gb(8.5 * GiB)).toBe('8.5 GB');
    expect(gb(120 * GiB)).toBe('120.0 GB');
    expect(gb(24 * MiB)).toBe('24 MB');
    expect(gb(2 * MiB)).toBe('2 MB');
    expect(gb(0)).toBe('0 MB');
    expect(gb(undefined)).toBe('0 MB');
  });

  it('fit badges say where a model runs; unknown shows nothing', () => {
    expect(fitText({ target: 'gpu', fits: true, availableBytes: 0 })).toEqual({ text: 'Fits the GPU', long: 'Fits the GPU.', tone: 'ok' });
    expect(fitText({ target: 'cpu', fits: true, availableBytes: 0 })).toEqual({
      text: 'Runs on the CPU (slow)',
      long: 'Runs on the CPU (slow): no GPU has room for it, so llama.cpp keeps it in RAM.',
      tone: 'warn',
    });
    expect(fitText({ target: 'none', fits: false, availableBytes: 0 })).toEqual({ text: 'Not recommended', long: 'Not recommended: too big for this PC.', tone: 'err' });
    expect(fitText({ target: 'unknown', fits: true, availableBytes: 0 })).toBe(undefined);
    expect(fitText(undefined)).toBe(undefined);
  });

  it('with the size and the memory measured the long form says the numbers; the badge stays short', () => {
    const none = fitText({ target: 'none', fits: false, availableBytes: 15 * GiB }, 80 * GiB)!;
    expect(none.text).toBe('Not recommended');
    expect(none.long).toBe('Not recommended: too big for this PC (needs about 80.0 GB, 15.0 GB free).');
    const cpu = fitText({ target: 'cpu', fits: true, availableBytes: 15 * GiB }, 12 * GiB)!;
    expect(cpu.text).toBe('Runs on the CPU (slow)');
    expect(cpu.long).toBe('Runs on the CPU (slow): no GPU has room for it, so llama.cpp keeps it in RAM (needs about 12.0 GB, 15.0 GB free).');
    expect(fitText({ target: 'gpu', fits: true, availableBytes: 14 * GiB }, 2 * MiB)!.long).toBe('Fits the GPU (needs about 2 MB, 14.0 GB free).');
    // half the numbers is no number: the plain sentence
    expect(fitText({ target: 'none', fits: false, availableBytes: 0 }, 80 * GiB)!.long).toBe('Not recommended: too big for this PC.');
    expect(fitText({ target: 'none', fits: false, availableBytes: 15 * GiB }, 0)!.long).toBe('Not recommended: too big for this PC.');
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
    expect(runtimeLine({ installed: true, tag: 'b11669', backend: 'vulkan', sourceName: 'llama.cpp (official weekly build)', devices: { probed: false, devices: [] } }, label)).toBe(
      'llama.cpp b11669 · Vulkan · llama.cpp (official weekly build)',
    );
    expect(deviceLine({ id: 'CUDA0', kind: 'CUDA', name: 'NVIDIA GeForce RTX 4060 Ti', totalBytes: 16 * GiB, freeBytes: 14 * GiB })).toBe('CUDA0: NVIDIA GeForce RTX 4060 Ti, 14.0 GB free of 16.0 GB');
    expect(gpus([{ id: 'CPU', kind: 'CPU', name: 'cpu', totalBytes: 0, freeBytes: 0 }, { id: 'Vulkan0', kind: 'Vulkan', name: 'g', totalBytes: 1, freeBytes: 1 }]).map((d) => d.id)).toEqual(['Vulkan0']);
  });

  it('the memory line leaves out what is not known', () => {
    const v = { memory: { known: true, totalBytes: 32 * GiB, availableBytes: 20 * GiB }, modelsBytes: 26 * MiB, freeBytes: 120 * GiB } as LocalView;
    expect(memoryLine(v)).toBe('RAM: 20.0 GB free of 32.0 GB · Models on disk: 26 MB · Free disk: 120.0 GB');
    expect(memoryLine({ ...v, memory: { known: false, totalBytes: 0, availableBytes: 0 }, freeBytes: 0 })).toBe('Models on disk: 26 MB');
  });

  it('whole numbers from a settings box', () => {
    expect(wholeNumber('65536')).toBe(65536);
    expect(wholeNumber(' 0 ')).toBe(0);
    expect(wholeNumber('')).toBe(undefined);
    expect(wholeNumber('12.5')).toBe(undefined);
    expect(wholeNumber('-1')).toBe(undefined);
    expect(wholeNumber('abc')).toBe(undefined);
  });
});
