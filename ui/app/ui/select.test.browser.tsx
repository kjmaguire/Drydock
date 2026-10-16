// The kit's dropdown: roles, label association, mouse and keyboard, and the test helpers that drive it.
import { expect } from 'remix/assert';
import type { Handle } from 'remix/component';
import { afterEach, describe, it } from 'remix/test';
import { byLabel, choose, cleanup, optionsOf, renderPlain, type, valueOf, waitFor } from '../test/render.tsx';
import { Field } from './form.tsx';
import { Select, type SelectOption } from './select.tsx';

afterEach(cleanup);

const EFFORTS: SelectOption[] = [
  { value: '', label: 'adapter default' },
  { value: 'low', label: 'low' },
  { value: 'medium', label: 'medium', hint: 'recommended' },
  { value: 'high', label: 'high' },
  { value: 'max', label: 'max', disabled: true },
];

const press = (target: EventTarget, key: string, init: KeyboardEventInit = {}) => {
  const e = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init });
  target.dispatchEvent(e);
  return e;
};

type Over = Partial<Parameters<typeof Select>[0]['props']>;

/** A form the way the screens write one: the value is the form's own state, re-rendered on change. */
function Host(handle: Handle<{ value: string; over: Over; onChange: (v: string) => void }>) {
  let current = handle.props.value;
  return () => (
    <Field label="Effort">
      {(id) => (
        <Select
          id={id}
          value={current}
          options={EFFORTS}
          onChange={(v) => {
            current = v;
            handle.props.onChange(v);
            void handle.update();
          }}
          {...handle.props.over}
        />
      )}
    </Field>
  );
}

function show(value = 'low', over: Over = {}) {
  const changes: string[] = [];
  const r = renderPlain(<Host value={value} over={over} onChange={(v) => changes.push(v)} />);
  const trigger = () => byLabel<HTMLButtonElement>(r.container, 'Effort');
  const list = () => document.getElementById(trigger().getAttribute('aria-controls')!)!;
  const options = () => Array.from(list().querySelectorAll<HTMLElement>('[role="option"]'));
  const highlighted = () => options().find((o) => o.getAttribute('data-highlighted') === 'true')?.getAttribute('data-value');
  const isOpen = () => trigger().getAttribute('aria-expanded') === 'true' && list().closest('[popover]')!.matches(':popover-open');
  return { r, changes, trigger, list, options, highlighted, isOpen };
}

describe('Select', () => {
  it('is a combobox button named by its Field label, showing the chosen option, over a listbox of options', () => {
    const { r, trigger, list, options } = show('medium');
    const b = trigger();
    expect(b.tagName).toBe('BUTTON');
    expect(b.getAttribute('role')).toBe('combobox');
    expect(b.getAttribute('aria-haspopup')).toBe('listbox');
    expect(b.getAttribute('aria-expanded')).toBe('false');
    expect(b.querySelector('.value')!.textContent).toBe('mediumrecommended');
    expect(b.getAttribute('data-value')).toBe('medium');
    expect(list().getAttribute('role')).toBe('listbox');
    expect(options().map((o) => o.getAttribute('aria-selected'))).toEqual(['false', 'false', 'true', 'false', 'false']);
    expect(options()[4]!.getAttribute('aria-disabled')).toBe('true');
    // closed: the list is a hidden popover, so it is not in the page's accessibility tree
    expect(list().closest('[popover]')!.matches(':popover-open')).toBe(false);
    expect(r.container.querySelector('select')).toBeNull();
  });

  it('shows the placeholder when the value matches no option', () => {
    const { trigger } = show('', { options: EFFORTS.slice(1), placeholder: 'Choose an effort' });
    const v = trigger().querySelector<HTMLElement>('.value')!;
    expect(v.textContent).toBe('Choose an effort');
    expect(v.getAttribute('data-placeholder')).toBe('true');
  });

  it('opens on click with the chosen option highlighted and focus in the list; an option click chooses it and closes', async () => {
    const { r, changes, trigger, list, options, highlighted, isOpen } = show('low');
    await r.act(() => trigger().click());
    expect(isOpen()).toBe(true);
    expect(highlighted()).toBe('low');
    expect(trigger().getAttribute('aria-activedescendant')).toBe(options()[1]!.id);
    await waitFor(() => expect(document.activeElement).toBe(list()), r);
    await r.act(() => options()[3]!.click());
    expect(changes).toEqual(['high']);
    expect(isOpen()).toBe(false);
    expect(trigger().getAttribute('data-value')).toBe('high');
    expect(trigger().querySelector('.value')!.textContent).toBe('high');
    await waitFor(() => expect(document.activeElement).toBe(trigger()), r);
    // the same option again changes nothing; a disabled one is never chosen
    await r.act(() => trigger().click());
    await r.act(() => options()[3]!.click());
    await r.act(() => trigger().click());
    await r.act(() => options()[4]!.click());
    expect(changes).toEqual(['high']);
    expect(isOpen()).toBe(false);
  });

  it('keyboard: arrows open and move, Home and End jump, Enter chooses, Escape closes and returns focus', async () => {
    const { r, changes, trigger, list, highlighted, isOpen } = show('low');
    trigger().focus();
    expect(press(trigger(), 'ArrowDown').defaultPrevented).toBe(true);
    r.root.flush();
    expect(isOpen()).toBe(true);
    expect(highlighted()).toBe('low');
    press(list(), 'ArrowDown');
    r.root.flush();
    expect(highlighted()).toBe('medium');
    press(list(), 'End');
    r.root.flush();
    expect(highlighted()).toBe('high'); // max is disabled
    press(list(), 'ArrowDown');
    r.root.flush();
    expect(highlighted()).toBe('high'); // no wrap past the end
    press(list(), 'Home');
    r.root.flush();
    expect(highlighted()).toBe('');
    press(list(), 'ArrowUp');
    r.root.flush();
    expect(highlighted()).toBe('');
    press(list(), 'ArrowDown');
    press(list(), 'ArrowDown');
    r.root.flush();
    press(list(), 'Enter');
    r.root.flush();
    expect(changes).toEqual(['medium']);
    expect(isOpen()).toBe(false);
    await waitFor(() => expect(document.activeElement).toBe(trigger()), r);
    // Escape closes without choosing
    press(trigger(), 'ArrowUp');
    r.root.flush();
    expect(isOpen()).toBe(true);
    press(list(), 'ArrowDown');
    r.root.flush();
    press(list(), 'Escape');
    await waitFor(() => expect(isOpen()).toBe(false), r);
    expect(changes).toEqual(['medium']);
    await waitFor(() => expect(document.activeElement).toBe(trigger()), r);
  });

  it('typing jumps to the option that starts with the letters: in the open list it highlights, on the closed button it chooses', async () => {
    const { r, changes, trigger, list, highlighted } = show('');
    await r.act(() => trigger().click());
    press(list(), 'm');
    press(list(), 'e');
    r.root.flush();
    expect(highlighted()).toBe('medium');
    press(list(), 'Escape');
    await waitFor(() => expect(trigger().getAttribute('aria-expanded')).toBe('false'), r);
    await r.act(() => trigger().click());
    press(list(), 'h');
    r.root.flush();
    expect(highlighted()).toBe('high');
    press(list(), 'x'); // "hx" matches nothing: the highlight stays
    r.root.flush();
    expect(highlighted()).toBe('high');
    press(list(), 'Escape');
    await waitFor(() => expect(trigger().getAttribute('aria-expanded')).toBe('false'), r);
    expect(changes).toEqual([]);
    press(trigger(), 'l');
    r.root.flush();
    expect(changes).toEqual(['low']);
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
  });

  it('a disabled control neither opens nor chooses', async () => {
    const { r, changes, trigger, isOpen } = show('low', { disabled: true });
    expect(trigger().disabled).toBe(true);
    await r.act(() => trigger().click());
    expect(isOpen()).toBe(false);
    press(trigger(), 'h');
    r.root.flush();
    expect(changes).toEqual([]);
  });

  it('takes an accessible name of its own and a hidden input for a named form value', () => {
    const r = renderPlain(<Select label="Quota window" name="window" value="rpm" options={[{ value: 'rpm', label: 'per minute' }]} onChange={() => {}} />);
    const b = byLabel<HTMLButtonElement>(r.container, 'Quota window');
    expect(b.getAttribute('role')).toBe('combobox');
    // the list is reached from the button (aria-controls) and carries no name of its own: a query by label finds one control
    expect(document.getElementById(b.getAttribute('aria-controls')!)!.hasAttribute('aria-label')).toBe(false);
    expect(Array.from(document.querySelectorAll('[aria-label="Quota window"]')).length).toBe(1);
    const hidden = r.container.querySelector<HTMLInputElement>('input[type="hidden"][name="window"]')!;
    expect(hidden.value).toBe('rpm');
  });

  it('the test helpers: choose() and type() pick by value without opening; optionsOf() and valueOf() read it back', () => {
    const { r, changes, trigger, isOpen } = show('low');
    expect(valueOf(trigger())).toBe('low');
    expect(optionsOf(trigger())).toEqual([
      { value: '', label: 'adapter default', disabled: false },
      { value: 'low', label: 'low', disabled: false },
      { value: 'medium', label: 'mediumrecommended', disabled: false },
      { value: 'high', label: 'high', disabled: false },
      { value: 'max', label: 'max', disabled: true },
    ]);
    choose(trigger(), 'high');
    r.root.flush();
    expect(changes).toEqual(['high']);
    expect(isOpen()).toBe(false);
    type(trigger(), 'medium');
    r.root.flush();
    expect(changes).toEqual(['high', 'medium']);
    expect(valueOf(trigger())).toBe('medium');
    expect(() => choose(trigger(), 'nope')).toThrow(/no option "nope"/);
  });
});
