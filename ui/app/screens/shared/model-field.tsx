// The model control of the agent forms, the chat dialogs and model routing: the adapter's model aliases as a themed list,
// with "Another model..." for a full model name typed in (full names are accepted too). It replaced an <input list=...>
// whose suggestion popup WebView2 drew itself, in the system style and where the OS put it, not under the field.

import { css, on, ref, type Handle } from 'remix/component';
import { Button } from '../../ui/button.tsx';
import { Select, type SelectOption } from '../../ui/select.tsx';

/** The list entry that turns the control into a text field; never a model name (the core refuses names starting with "-"). */
export const OTHER_MODEL = '__other__';

const typedCss = css({ display: 'grid', gap: '4px', minWidth: '0', '& button': { justifySelf: 'start' } });

export function ModelField(
  handle: Handle<{
    id?: string;
    /** The accessible name when no <label for> names the control. */
    label?: string;
    value: string;
    /** The adapter's aliases; none makes the control a plain text field. */
    models: readonly string[];
    /** The label of the empty value ("adapter default"); without one an empty value shows `placeholder`. */
    emptyLabel?: string;
    placeholder?: string;
    disabled?: boolean;
    onChange: (v: string) => void;
  }>,
) {
  let typing = false; // "Another model..." was chosen
  let listKey = '';
  let input: HTMLInputElement | undefined;
  return () => {
    const { id, label, value, models, emptyLabel, placeholder, disabled, onChange } = handle.props;
    const key = models.join('\n');
    if (key !== listKey) {
      // another adapter's list: back to it
      listKey = key;
      typing = false;
    }
    if (models.length === 0) {
      return (
        <input
          id={id}
          aria-label={label}
          value={value}
          disabled={disabled}
          spellcheck={false}
          autocomplete="off"
          placeholder={placeholder}
          mix={on('input', (e) => onChange(e.currentTarget.value))}
        />
      );
    }
    if (typing || (value !== '' && !models.includes(value))) {
      return (
        <div mix={typedCss}>
          <input
            id={id}
            aria-label={label}
            value={value}
            disabled={disabled}
            spellcheck={false}
            autocomplete="off"
            placeholder="the model's full name"
            data-testid="model-typed"
            mix={[ref((n) => (input = n as HTMLInputElement)), on('input', (e) => onChange(e.currentTarget.value))]}
          />
          <Button
            variant="link"
            size="sm"
            disabled={disabled}
            mix={on('click', () => {
              typing = false;
              if (value !== '' && !models.includes(value)) onChange('');
              else void handle.update();
            })}
          >
            Choose from the list instead
          </Button>
        </div>
      );
    }
    const options: SelectOption[] = [
      ...(emptyLabel !== undefined ? [{ value: '', label: emptyLabel }] : []),
      ...models.map((m) => ({ value: m, label: m })),
      { value: OTHER_MODEL, label: 'Another model...', hint: 'type its full name' },
    ];
    return (
      <Select
        id={id}
        label={label}
        value={value}
        options={options}
        disabled={disabled}
        placeholder={placeholder}
        onChange={(v) => {
          if (v !== OTHER_MODEL) return onChange(v);
          typing = true;
          void handle.update();
          handle.queueTask(() => input?.focus());
        }}
      />
    );
  };
}
