// The model control: the adapter's aliases as a themed list with a typed entry, and the layout of the model and effort pair.
import { css, type Handle } from 'remix/component';
import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Capabilities } from '../../api/types.ts';
import { byLabel, byText, choose, cleanup, optionsOf, renderPlain, type, valueOf, waitFor } from '../../test/render.tsx';
import { Field, formGrid } from '../../ui/form.tsx';
import { ModelEffortFields } from '../team/fields.tsx';
import { ModelField, OTHER_MODEL } from './model-field.tsx';

afterEach(cleanup);

const MODELS = ['opus', 'sonnet', 'haiku'];

function Host(handle: Handle<{ value: string; models: readonly string[]; onChange: (v: string) => void }>) {
  let current = handle.props.value;
  return () => (
    <Field label="Model">
      {(id) => (
        <ModelField
          id={id}
          value={current}
          models={handle.props.models}
          emptyLabel="adapter default"
          onChange={(v) => {
            current = v;
            handle.props.onChange(v);
            void handle.update();
          }}
        />
      )}
    </Field>
  );
}

function show(value = '', models: readonly string[] = MODELS) {
  const changes: string[] = [];
  const r = renderPlain(<Host value={value} models={models} onChange={(v) => changes.push(v)} />);
  return { r, changes, model: () => byLabel<HTMLElement>(r.container, 'Model') };
}

describe('ModelField', () => {
  it('lists the adapter default, the aliases and a typed entry; an alias is chosen from the list', () => {
    const { r, changes, model } = show();
    expect(model().getAttribute('role')).toBe('combobox');
    expect(optionsOf(model()).map((o) => o.value)).toEqual(['', 'opus', 'sonnet', 'haiku', OTHER_MODEL]);
    expect(optionsOf(model())[4]!.label).toBe('Another model...type its full name');
    choose(model(), 'opus');
    r.root.flush();
    expect(changes).toEqual(['opus']);
    expect(valueOf(model())).toBe('opus');
    expect(r.container.querySelector('input')).toBeNull();
  });

  it('"Another model..." turns the control into a text field, focused, that goes back to the list on request', async () => {
    const { r, changes, model } = show('sonnet');
    choose(model(), OTHER_MODEL);
    r.root.flush();
    const input = model() as HTMLInputElement;
    expect(input.tagName).toBe('INPUT');
    expect(input.getAttribute('data-testid')).toBe('model-typed');
    expect(input.value).toBe('sonnet');
    await waitFor(() => expect(document.activeElement).toBe(input), r);
    expect(changes).toEqual([]); // nothing changed yet
    type(input, 'claude-sonnet-4-5-20250929');
    r.root.flush();
    expect(changes).toEqual(['claude-sonnet-4-5-20250929']);
    // back to the list: a full name that is not an alias is cleared
    await r.act(() => byText(r.container, 'button', 'Choose from the list instead').click());
    expect(changes).toEqual(['claude-sonnet-4-5-20250929', '']);
    expect(model().getAttribute('role')).toBe('combobox');
    expect(valueOf(model())).toBe('');
  });

  it('a saved full name that is no alias shows as text from the start; no aliases at all means a plain text field', () => {
    const a = show('claude-opus-4-1');
    expect((a.model() as HTMLInputElement).tagName).toBe('INPUT');
    expect((a.model() as HTMLInputElement).value).toBe('claude-opus-4-1');
    expect(byText(a.r.container, 'button', 'Choose from the list instead')).toBeTruthy();
    cleanup();
    const b = show('', []);
    expect((b.model() as HTMLInputElement).tagName).toBe('INPUT');
    expect(b.r.container.querySelector('button')).toBeNull();
  });
});

const wide = css({ width: '640px' });
const caps = { modelSelection: true, effortControl: true, efforts: ['low', 'medium', 'high'], models: MODELS } as Capabilities;

function Pair(handle: Handle<{ onChange: (m: string, e: string) => void }>) {
  let model = '';
  let effort = '';
  return () => (
    <form mix={[formGrid, wide]} data-testid="pair">
      <ModelEffortFields
        caps={caps}
        model={model}
        effort={effort}
        onModel={(v) => ((model = v), handle.props.onChange(model, effort), void handle.update())}
        onEffort={(v) => ((effort = v), handle.props.onChange(model, effort), void handle.update())}
      />
    </form>
  );
}

describe('the model and effort fields in a form grid', () => {
  it('the Model control stays inside its own column, level with Effort, before and after a model is chosen', () => {
    const picked: string[] = [];
    const r = renderPlain(<Pair onChange={(m) => picked.push(m)} />);
    const within = () => {
      const model = byLabel<HTMLElement>(r.container, 'Model');
      const effort = byLabel<HTMLElement>(r.container, 'Effort');
      const field = model.closest('div')!.parentElement!; // the Field's grid cell (label + control)
      const m = model.getBoundingClientRect();
      const f = field.getBoundingClientRect();
      const e = effort.getBoundingClientRect();
      const form = r.container.querySelector<HTMLElement>('[data-testid="pair"]')!.getBoundingClientRect();
      expect(m.width).toBeGreaterThan(100);
      expect(m.left).toBeGreaterThanOrEqual(f.left - 0.5);
      expect(m.right).toBeLessThanOrEqual(f.right + 0.5);
      expect(m.right).toBeLessThanOrEqual(e.left); // Model sits in the column to the left of Effort...
      expect(Math.abs(m.top - e.top)).toBeLessThan(1); // ...on the same row
      expect(m.right).toBeLessThan(form.right - 200); // and never drifts to the far right of the form
    };
    within();
    choose(byLabel<HTMLElement>(r.container, 'Model'), 'opus');
    r.root.flush();
    expect(picked).toEqual(['opus']);
    expect(valueOf(byLabel<HTMLElement>(r.container, 'Model'))).toBe('opus');
    within();
    choose(byLabel<HTMLElement>(r.container, 'Effort'), 'high');
    r.root.flush();
    within();
  });
});
