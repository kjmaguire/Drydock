import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { showBootFailure, showRuntimeFailure, startGuarded } from './failure.ts';

// The failure surfaces are plain DOM, so they are checked on the real document. Each test removes what it added.
afterEach(() => {
  for (const id of ['drydock-boot-failure', 'drydock-runtime-failure']) document.getElementById(id)?.remove();
});

const byTestId = (id: string) => document.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const button = (root: ParentNode, text: string) => Array.from(root.querySelectorAll('button')).find((b) => b.textContent === text) as HTMLButtonElement;

describe('the boot failure page (L42)', () => {
  it('covers the window with the error text and a Reload button', () => {
    const reloads: number[] = [];
    const page = showBootFailure(document, new TypeError('Failed to execute replaceState'), { reload: () => void reloads.push(1) });
    expect(page.getAttribute('role')).toBe('alert');
    expect(page.querySelector('h1')!.textContent).toBe('Drydock could not start');
    expect(byTestId('boot-failure-text')!.textContent).toBe('TypeError: Failed to execute replaceState');
    expect(getComputedStyle(page).position).toBe('fixed');
    expect(reloads).toEqual([]);
    button(page, 'Reload').click();
    expect(reloads).toEqual([1]);
  });

  it('shows the text as text, never as markup', () => {
    showBootFailure(document, new Error('<img src=x onerror=window.__boot=1> <b>bold</b>'));
    expect(byTestId('boot-failure-text')!.textContent).toBe('<img src=x onerror=window.__boot=1> <b>bold</b>');
    expect(document.querySelector('#drydock-boot-failure img, #drydock-boot-failure b')).toBeNull();
    expect((window as unknown as Record<string, unknown>).__boot).toBeUndefined();
  });

  it('is one page: showing it again replaces the text, and it takes the runtime bar with it', () => {
    showRuntimeFailure(document, new Error('first'));
    showBootFailure(document, new Error('second'));
    showBootFailure(document, new Error('third'));
    expect(document.querySelectorAll('#drydock-boot-failure')).toHaveLength(1);
    expect(byTestId('boot-failure-text')!.textContent).toBe('third');
    expect(document.getElementById('drydock-runtime-failure')).toBeNull();
  });
});

describe('startGuarded (L42)', () => {
  it('runs the start-up and shows nothing when it works', async () => {
    let ran = 0;
    expect(await startGuarded(document, async () => void ran++)).toBe(true);
    expect(ran).toBe(1);
    expect(document.getElementById('drydock-boot-failure')).toBeNull();
  });

  it('shows the failure page when the start-up throws, or rejects, instead of leaving a blank window', async () => {
    const errors: unknown[][] = [];
    const real = console.error;
    console.error = (...a: unknown[]) => void errors.push(a);
    try {
      expect(
        await startGuarded(document, async () => {
          throw new Error('the first render failed');
        }),
      ).toBe(false);
      expect(byTestId('boot-failure-text')!.textContent).toBe('the first render failed');
      expect(errors).toHaveLength(1); // and the console has it too
      expect(
        await startGuarded(document, () => {
          throw new RangeError('thrown before the first await');
        }),
      ).toBe(false);
      expect(byTestId('boot-failure-text')!.textContent).toBe('RangeError: thrown before the first await');
    } finally {
      console.error = real;
    }
  });
});

describe('the runtime failure bar (H11)', () => {
  it('shows an unhandled error with Reload and Dismiss, as one bar that later errors update', () => {
    const reloads: number[] = [];
    const bar = showRuntimeFailure(document, new TypeError('first'), { reload: () => void reloads.push(1) });
    expect(bar.getAttribute('role')).toBe('alert');
    expect(byTestId('runtime-failure-text')!.textContent).toBe('TypeError: first');
    showRuntimeFailure(document, new Error('second'));
    expect(document.querySelectorAll('#drydock-runtime-failure')).toHaveLength(1);
    expect(byTestId('runtime-failure-text')!.textContent).toBe('second');
    button(bar, 'Reload').click();
    expect(reloads).toEqual([1]);
    button(bar, 'Dismiss').click();
    expect(document.getElementById('drydock-runtime-failure')).toBeNull();
    // the next error brings it back
    showRuntimeFailure(document, new Error('third'));
    expect(byTestId('runtime-failure-text')!.textContent).toBe('third');
  });
});
