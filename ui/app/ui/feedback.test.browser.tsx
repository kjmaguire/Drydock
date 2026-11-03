import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { RpcError } from '../api/transport.ts';
import { query, retryFor, services } from '../state/services.tsx';
import { cleanup, drain, mount, renderPlain, testServices, waitFor } from '../test/render.tsx';
import type { Handle } from 'remix/component';
import { coreDown, ErrorNote, errorText } from './feedback.tsx';

afterEach(() => {
  coreDown.set(false);
  cleanup();
});

const network = () => new RpcError('task.list', 'network', 'Failed to fetch');

describe('ErrorNote (G5: every failed read offers Retry)', () => {
  it('has no button without retry, and Retry calls the read again', async () => {
    const plain = renderPlain(<ErrorNote error={new Error('no such task')} />);
    expect(plain.$('[role="alert"]')!.textContent).toBe('no such task');
    expect(plain.$('[role="alert"] button')).toBe(null);

    let calls = 0;
    const r = renderPlain(<ErrorNote error={new Error('boom')} what="Tasks" retry={() => calls++} />);
    const btn = r.$('[role="alert"] button')!;
    expect(btn.textContent).toBe('Retry');
    btn.click();
    await drain();
    expect(calls).toBe(1);
  });

  it('shows a caller action beside Retry', () => {
    const r = renderPlain(<ErrorNote error={new Error('x')} action={<a href="/settings">Settings</a>} />);
    expect(r.$('[role="alert"] a')!.textContent).toBe('Settings');
  });

  it('says the core did not answer for a network error, and nothing while the shell banner says so (G4)', () => {
    expect(errorText(network())).toBe("Drydock's core did not answer.");
    expect(errorText(new RpcError('x', 'bad_request', 'refused'))).toBe('refused');
    const r = renderPlain(
      <div>
        <ErrorNote error={network()} />
        <ErrorNote error={new Error('the core refused it')} />
      </div>,
    );
    expect(Array.from(r.$$('[role="alert"]')).map((n) => n.textContent)).toEqual(["Drydock's core did not answer.", 'the core refused it']);
    coreDown.set(true);
    r.root.flush();
    expect(Array.from(r.$$('[role="alert"]')).map((n) => n.textContent)).toEqual(['the core refused it']);
    coreDown.set(false);
    r.root.flush();
    expect(r.$$('[role="alert"]').length).toBe(2);
  });
});

describe('retryFor', () => {
  it('is the reload of the read whose error is shown, never an action', () => {
    let reloaded = 0;
    const read = { error: new Error('read failed'), reload: async () => void reloaded++ };
    const act = { error: new Error('save failed') };
    expect(retryFor(act, read)).toBe(undefined);
    expect(retryFor({ error: undefined }, read)).not.toBe(undefined);
    void retryFor(read, act)!();
    expect(reloaded).toBe(1);
    expect(retryFor({ error: undefined, reload: async () => {} })).toBe(undefined);
  });

  it('a query that failed reads again from its note', async () => {
    let n = 0;
    function Reader(handle: Handle) {
      const s = services(handle);
      const q = query(handle, { load: async () => (n++ === 0 ? Promise.reject(new Error('first read failed')) : 'second read') });
      void s;
      return () => <div>{q.error ? <ErrorNote error={q.error} retry={retryFor(q)} /> : <p data-testid="data">{q.data}</p>}</div>;
    }
    const r = mount(<Reader />, testServices());
    const btn = await waitFor(() => {
      const el = r.$('[role="alert"] button');
      if (!el) throw new Error('not yet');
      return el as HTMLElement;
    }, r);
    expect(btn.textContent).toBe('Retry');
    btn.click();
    const p = await waitFor(() => {
      const el = r.$('[data-testid="data"]');
      if (!el) throw new Error('not yet');
      return el;
    }, r);
    expect(p.textContent).toBe('second read');
  });
});
