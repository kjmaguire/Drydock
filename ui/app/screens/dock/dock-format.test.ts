import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { createDockClient, decodeBase64 } from '../../api/dockClient.ts';
import { FakeTransport } from '../../test/fakes.ts';
import { diffTotals, formatSize, LogBuffer, parseColor, splitPath, toHex, withAlpha } from './dock-format.ts';

describe('the dock client', () => {
  it('decodes terminal output from base64 and turns null lists into empty ones', async () => {
    const t = new FakeTransport();
    t.responses.set('terminal.read', { data: btoa('héllo'.normalize()), next: 6, exited: false, exitCode: 0 });
    t.responses.set('logs.list', null);
    t.responses.set('task.diffFiles', { source: 'none', files: null, liveAvailable: false });
    const c = createDockClient(t);
    const r = await c.terminalRead('term_1', 0, 10);
    expect(r.data.length).toBeGreaterThan(0);
    expect(r.dropped).toBe(0);
    expect(await c.logsList('t')).toEqual([]);
    expect((await c.taskDiffFiles('t')).files).toEqual([]);
    expect(t.calls.find((x) => x.method === 'task.diffFiles')?.params).toEqual({ id: 't', live: false });
    expect(Array.from(decodeBase64(btoa('ab')))).toEqual([97, 98]);
    expect(decodeBase64(null).length).toBe(0);
  });
});

describe('dock format helpers', () => {
  it('formats sizes', () => {
    expect(formatSize(512)).toBe('512 bytes');
    expect(formatSize(2048)).toBe('2 KB');
    expect(formatSize(3 << 20)).toBe('3.0 MB');
  });

  it('sums a file list, binary files adding no lines', () => {
    expect(
      diffTotals([
        { path: 'a', status: 'modified', additions: 2, deletions: 1 },
        { path: 'b.png', status: 'added', additions: 9, deletions: 9, binary: true },
        { path: 'c', status: 'deleted', additions: 0, deletions: 4 },
      ]),
    ).toEqual({ files: 3, additions: 2, deletions: 5 });
  });

  it('splits a path into name and directory', () => {
    expect(splitPath('calc.go')).toEqual({ name: 'calc.go', dir: '' });
    expect(splitPath('internal/core/calc.go')).toEqual({ name: 'calc.go', dir: 'internal/core' });
  });

  it('parses computed colors and converts them for Monaco and xterm', () => {
    expect(parseColor('rgb(1, 2, 3)')).toEqual({ r: 1, g: 2, b: 3, a: 1 });
    expect(parseColor('rgba(255, 0, 16, 0.5)')).toEqual({ r: 255, g: 0, b: 16, a: 0.5 });
    expect(parseColor('rgb(10 20 30 / 25%)')).toEqual({ r: 10, g: 20, b: 30, a: 0.25 });
    expect(parseColor('#5e6ad2')).toEqual({ r: 94, g: 106, b: 210, a: 1 });
    expect(parseColor('oklch(0.5 0.1 200)')).toBeUndefined();
    expect(toHex('rgb(19, 20, 24)')).toBe('#131418');
    expect(toHex('rgba(255, 255, 255, 0.045)')).toBe('#ffffff0b');
    expect(toHex('nonsense')).toBeUndefined();
    expect(withAlpha('rgb(110, 121, 232)', 0.35)).toBe('rgba(110, 121, 232, 0.35)');
  });
});

describe('the log buffer', () => {
  it('keeps a trailing partial line until the rest arrives', () => {
    const b = new LogBuffer();
    b.append('one\ntw');
    expect(b.lines).toEqual(['one']);
    expect(b.text()).toBe('one\ntw');
    b.append('o\nthree\n');
    expect(b.lines).toEqual(['one', 'two', 'three']);
    expect(b.text()).toBe('one\ntwo\nthree');
    b.append('');
    expect(b.lines.length).toBe(3);
  });

  it('keeps at most max lines: drops the oldest when appending and the newest when prepending', () => {
    const b = new LogBuffer(3);
    b.append('1\n2\n3\n4\n');
    expect(b.lines).toEqual(['2', '3', '4']);
    b.prepend('0\n1\n');
    expect(b.lines).toEqual(['0', '1', '2']);
    expect(b.text()).toBe('0\n1\n2');
  });
});
