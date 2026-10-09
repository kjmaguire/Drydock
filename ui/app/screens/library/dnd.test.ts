import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { DRAG_MIME, joinedNote, readDrag, startDrag, type DragLike } from './dnd.ts';

/** A DataTransfer that keeps what was set, as a browser does between dragstart and drop. */
function transfer(): DragLike & { data: Record<string, string> } {
  const data: Record<string, string> = {};
  return {
    data,
    dataTransfer: { setData: (k: string, v: string) => void (data[k] = v), getData: (k: string) => data[k] ?? '', effectAllowed: 'none' },
  };
}

describe('library drag and drop', () => {
  it('a dragged item carries its kind and id, and text for browsers that need it', () => {
    const e = transfer();
    startDrag(e, { kind: 'mcp', id: 'tst' });
    expect(e.dataTransfer!.effectAllowed).toBe('copy');
    expect(e.data['text/plain']).toBe('mcp:tst');
    expect(readDrag(e)).toEqual({ kind: 'mcp', id: 'tst' });
  });

  it('anything that is not one of our items is ignored', () => {
    const empty = transfer();
    expect(readDrag(empty)).toBeUndefined();
    const text = transfer();
    text.data['text/plain'] = 'https://example.com';
    expect(readDrag(text)).toBeUndefined();
    for (const raw of ['not json', '{"kind":"file","id":"x"}', '{"kind":"mcp","id":""}', '{"kind":"template","id":3}']) {
      const e = transfer();
      e.data[DRAG_MIME] = raw;
      expect(readDrag(e)).toBeUndefined();
    }
    expect(readDrag({ dataTransfer: null })).toBeUndefined();
  });

  it('the note after applying a template names what was granted and what was not', () => {
    const name = (id: string) => (id === 'tst' ? 'Test server' : id);
    expect(joinedNote('Pay', 'Beta', [], [], name)).toBe('Pay joined Beta.');
    expect(joinedNote('Pay', 'Beta', ['tst'], ['docs: missing'], name)).toBe('Pay joined Beta, with Test server. Not granted: docs: missing.');
  });
});
