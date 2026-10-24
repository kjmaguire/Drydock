import { expect } from 'remix/assert';
import type { Handle } from 'remix/component';
import { afterEach, describe, it } from 'remix/test';
import { byLabel, cleanup, renderPlain } from '../test/render.tsx';
import { Switch } from './form.tsx';

afterEach(cleanup);

function Host(handle: Handle<{ picks: boolean[]; disabled?: boolean }>) {
  let on = false;
  return () => (
    <Switch
      label={<span class="sr-only">Let codex use the gateway</span>}
      checked={on}
      disabled={handle.props.disabled}
      onChange={(v) => {
        handle.props.picks.push(v);
        on = v;
        void handle.update();
      }}
    />
  );
}

describe('Switch', () => {
  it('is a switch named by its label, reports a change, and its label is a 28 px target even when only a screen reader reads it', () => {
    const picks: boolean[] = [];
    const { container, root } = renderPlain(<Host picks={picks} />);
    const sw = byLabel<HTMLButtonElement>(container, 'Let codex use the gateway');
    expect(sw.getAttribute('role')).toBe('switch');
    expect(sw.getAttribute('aria-checked')).toBe('false');
    sw.click();
    root.flush();
    expect(picks).toEqual([true]);
    expect(sw.getAttribute('aria-checked')).toBe('true');
    const label = container.querySelector(`label[for="${sw.id}"]`) as HTMLElement;
    expect(label.getBoundingClientRect().height >= 28).toBe(true);
  });

  it('a disabled switch says so and does not change', () => {
    const picks: boolean[] = [];
    const { container, root } = renderPlain(<Host picks={picks} disabled />);
    const sw = byLabel<HTMLButtonElement>(container, 'Let codex use the gateway');
    expect(sw.getAttribute('aria-disabled')).toBe('true');
    sw.click();
    root.flush();
    expect(picks).toEqual([]);
  });
});
