import { expect } from 'remix/assert';
import { createRoot, on, type Handle } from 'remix/component';
import { afterEach, describe, it } from 'remix/test';
import { drain } from '../test/render.tsx';
import { ErrorBoundary } from './error-boundary.tsx';

// The components below are TEST DOUBLES that throw on demand. The boundary has to show what failed where the component was,
// without the runtime ever seeing an error (the runtime would keep the old picture, or draw nothing on a first draw).

const mounted: { cleanup: () => void }[] = [];
afterEach(() => {
  for (const m of mounted.splice(0)) m.cleanup();
});

function mountChecked(node: JSX.Element) {
  const container = document.createElement('div');
  document.body.append(container);
  const root = createRoot(container);
  const errors: unknown[] = [];
  root.addEventListener('error', (e) => errors.push(e.error));
  root.render(node);
  root.flush();
  const out = {
    container,
    root,
    errors,
    $: (sel: string) => container.querySelector<HTMLElement>(sel),
    $$: (sel: string) => Array.from(container.querySelectorAll<HTMLElement>(sel)),
    flush: () => root.flush(),
    cleanup: () => {
      root.dispose();
      container.remove();
    },
  };
  mounted.push(out);
  return out;
}

/** Draws "ok <name> #<instance>"; throws in its render while `fail.on` is true. */
const fail = { on: false };
let instances = 0;
function Bomb(handle: Handle<{ name?: string; bad?: boolean }>) {
  const n = ++instances;
  return () => {
    if (fail.on || handle.props.bad) throw new TypeError(`${handle.props.name ?? 'bomb'} is not a function`);
    return <p data-testid="bomb-ok">{`ok ${handle.props.name ?? 'bomb'} #${n}`}</p>;
  };
}
function SetupBomb(): () => JSX.Element {
  throw new RangeError('setup failed');
}
function Counter(handle: Handle) {
  let n = 0;
  return () => (
    <button data-testid="counter" mix={on('click', () => (n++, void handle.update()))}>
      {n}
    </button>
  );
}

const panel = (m: { $: (s: string) => HTMLElement | null }) => m.$('[data-testid="region-error"]');

describe('ErrorBoundary', () => {
  it('shows what failed in place of a component that throws on its first draw, and the page around it still works', () => {
    fail.on = false;
    const m = mountChecked(
      <div>
        <p data-testid="before">before</p>
        <ErrorBoundary label="the thing">
          <Bomb name="widget" bad />
        </ErrorBoundary>
        <Counter />
      </div>,
    );
    expect(m.errors).toEqual([]); // the runtime saw nothing: the page was drawn
    const p = panel(m)!;
    expect(p.getAttribute('role')).toBe('alert');
    expect(p.textContent).toContain('Could not show the thing');
    expect(m.$('[data-testid="region-error-text"]')!.textContent).toBe('TypeError: widget is not a function');
    expect(Array.from(p.querySelectorAll('button')).map((b) => b.textContent)).toEqual(['Try again', 'Reload Drydock']);
    expect(m.$('[data-testid="before"]')!.textContent).toBe('before');
    m.$('[data-testid="counter"]')!.click();
    m.flush();
    expect(m.$('[data-testid="counter"]')!.textContent).toBe('1');
  });

  it('catches a setup that throws too', () => {
    const m = mountChecked(
      <ErrorBoundary>
        <SetupBomb />
      </ErrorBoundary>,
    );
    expect(m.errors).toEqual([]);
    expect(panel(m)!.textContent).toContain('Could not show this part of Drydock');
    expect(m.$('[data-testid="region-error-text"]')!.textContent).toBe('RangeError: setup failed');
  });

  it('looks through host elements, fragments and arrays for the components to guard', () => {
    const m = mountChecked(
      <ErrorBoundary>
        <section>
          <>
            {[<Bomb key="a" name="a" bad />, <div key="b">{[<Counter />]}</div>]}
          </>
        </section>
      </ErrorBoundary>,
    );
    expect(m.errors).toEqual([]);
    expect(m.$$('[data-testid="region-error"]')).toHaveLength(1);
    expect(m.$('section [data-testid="counter"]')).not.toBeNull();
  });

  it('catches a component that fails later, on an update, and Try again mounts it afresh', async () => {
    fail.on = false;
    function Page(handle: Handle) {
      return () => (
        <div>
          <button data-testid="break" mix={on('click', () => ((fail.on = true), void handle.update()))}>
            break
          </button>
          <ErrorBoundary label="the list">
            <Bomb name="row" />
          </ErrorBoundary>
        </div>
      );
    }
    const before = instances;
    const m = mountChecked(<Page />);
    expect(m.$('[data-testid="bomb-ok"]')!.textContent).toBe(`ok row #${before + 1}`);
    // the Page itself re-renders, and the component it holds fails
    m.$('[data-testid="break"]')!.click();
    m.flush();
    expect(m.errors).toEqual([]);
    expect(panel(m)!.textContent).toContain('Could not show the list');
    expect(m.$('[data-testid="bomb-ok"]')).toBeNull();
    // fixed meanwhile: Try again draws it with a fresh instance (a new setup), not the old one
    fail.on = false;
    (Array.from(panel(m)!.querySelectorAll('button')).find((b) => b.textContent === 'Try again') as HTMLButtonElement).click();
    m.flush();
    await drain();
    m.flush();
    expect(panel(m)).toBeNull();
    expect(m.$('[data-testid="bomb-ok"]')!.textContent).toBe(`ok row #${before + 2}`);
  });

  it('draws the component again by itself when its next render works', () => {
    function Page(handle: Handle) {
      let bad = true;
      return () => (
        <div>
          <button data-testid="fix" mix={on('click', () => ((bad = false), void handle.update()))}>
            fix
          </button>
          <ErrorBoundary>
            <Bomb bad={bad} />
          </ErrorBoundary>
        </div>
      );
    }
    fail.on = false;
    const m = mountChecked(<Page />);
    expect(panel(m)).not.toBeNull();
    m.$('[data-testid="fix"]')!.click();
    m.flush();
    expect(panel(m)).toBeNull();
    expect(m.$('[data-testid="bomb-ok"]')).not.toBeNull();
  });

  it('Reload Drydock calls what it was given (and only then)', () => {
    const reloads: number[] = [];
    const m = mountChecked(
      <ErrorBoundary reload={() => void reloads.push(1)}>
        <Bomb bad />
      </ErrorBoundary>,
    );
    expect(reloads).toEqual([]);
    (Array.from(panel(m)!.querySelectorAll('button')).find((b) => b.textContent === 'Reload Drydock') as HTMLButtonElement).click();
    expect(reloads).toEqual([1]);
  });

  it('writes out characters that are not drawn in the error text, and tells onError once per new error', () => {
    const seen: unknown[] = [];
    function Evil() {
      return () => {
        throw new Error('open ‮gnp.exe');
      };
    }
    function Page(handle: Handle) {
      return () => (
        <div>
          <button data-testid="again" mix={on('click', () => void handle.update())}>
            again
          </button>
          <ErrorBoundary onError={(e) => seen.push(e)}>
            <Evil />
          </ErrorBoundary>
        </div>
      );
    }
    const m = mountChecked(<Page />);
    expect(m.$('[data-testid="region-error-text"]')!.textContent).toBe('open \\u{202e}gnp.exe');
    m.$('[data-testid="again"]')!.click();
    m.flush();
    expect(seen).toHaveLength(1); // the same error on the next render is not reported again
  });

  it('a failed region tries again by itself when its reset key changes; a healthy one keeps its state', () => {
    function Page(handle: Handle<{ route: string }>) {
      return () => (
        <div>
          <button data-testid="go" mix={on('click', () => void handle.update())}>
            go
          </button>
          <ErrorBoundary resetKey={handle.props.route} label="this page">
            {handle.props.route === 'broken' ? <Bomb name="page" bad /> : <Counter />}
          </ErrorBoundary>
        </div>
      );
    }
    // healthy: the counter keeps its count across a change of key
    const healthy = mountChecked(<Page route="a" />);
    healthy.$('[data-testid="counter"]')!.click();
    healthy.flush();
    healthy.root.render(<Page route="b" />);
    healthy.flush();
    expect(healthy.$('[data-testid="counter"]')!.textContent).toBe('1');
    // failed: the next route draws, although the boundary was never asked to try again
    const failed = mountChecked(<Page route="broken" />);
    expect(panel(failed)).not.toBeNull();
    failed.root.render(<Page route="fine" />);
    failed.flush();
    expect(panel(failed)).toBeNull();
    expect(failed.$('[data-testid="counter"]')).not.toBeNull();
  });
});
