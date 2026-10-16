import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { RpcError } from '../api/transport.ts';
import { errorText, isAbort } from './failure.ts';

describe('errorText', () => {
  it('names the kind of error and says what it said', () => {
    expect(errorText(new TypeError('x is undefined'))).toBe('TypeError: x is undefined');
    expect(errorText(new Error('plain'))).toBe('plain');
    expect(errorText(new RpcError('task.get', 'network', 'Could not reach the Drydock core'))).toBe('RpcError: Could not reach the Drydock core');
    expect(errorText('just a string')).toBe('just a string');
    expect(errorText({ code: 7 })).toBe('{"code":7}');
    expect(errorText(undefined)).toBe('An error without a message');
    expect(errorText(new Error(''))).toBe('Error');
  });

  it('writes out characters that are not drawn and stops at 2,000 characters', () => {
    expect(errorText(new Error('open ‮gnp.exe'))).toBe('open \\u{202e}gnp.exe');
    const long = errorText(new Error('x'.repeat(5000)));
    expect(long.length).toBe(2000);
    expect(long.endsWith('…')).toBe(true);
  });

  it('survives a value that can not be written as text', () => {
    const cyclic: Record<string, unknown> = {};
    cyclic.self = cyclic;
    expect(() => errorText(cyclic)).not.toThrow();
  });
});

describe('isAbort', () => {
  it('knows a cancelled request from a failure', () => {
    expect(isAbort(new DOMException('aborted', 'AbortError'))).toBe(true);
    expect(isAbort(Object.assign(new Error('x'), { name: 'AbortError' }))).toBe(true);
    expect(isAbort(new TypeError('x'))).toBe(false);
    expect(isAbort(undefined)).toBe(false);
    expect(isAbort('AbortError')).toBe(false);
  });
});
