// A themed dropdown: a button that shows the chosen option and opens a listbox drawn with the design tokens, in both
// themes. It replaces the native <select>, whose option popup WebView2 draws itself in the system style (it ignored the app's
// theme and sat where the OS put it). Controlled: `value` plus `onChange`, like the native control the forms used.

import { css, on, ref, type Handle, type MixInput } from 'remix/component';
import * as popover from '@remix-run/ui/popover';
import { Check, ChevronDown } from 'lucide';
import { Icon } from './icon.tsx';
import { t } from './tokens.ts';

export interface SelectOption {
  value: string;
  label: string;
  /** Quiet text after the label (a reason, a detail). Part of the option's name. */
  hint?: string;
  disabled?: boolean;
}

export interface SelectProps {
  /** The id the label points at (from Field). */
  id?: string;
  value: string;
  options: readonly SelectOption[];
  onChange: (value: string, option: SelectOption) => void;
  disabled?: boolean;
  /** The accessible name when no <label for> names the control. */
  label?: string;
  /** Shown, quietly, when `value` matches no option (an empty value with no option for it: "Choose..."). */
  placeholder?: string;
  testId?: string;
  size?: 'md' | 'sm';
  title?: string;
  /** The chosen value takes part in FormData under this name (a hidden input). */
  name?: string;
  /** Inline style on the trigger (a width where the control is not a form field). */
  style?: Record<string, string>;
  /** Extra mixins on the trigger. */
  mix?: MixInput<HTMLButtonElement>[];
}

const triggerCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  width: '100%',
  // never wider than its column: a long choice ("private (may receive repository content)") is cut with an ellipsis and
  // read in full from the title, instead of the box growing over the field beside it
  minWidth: '0',
  maxWidth: '100%',
  minHeight: '34px',
  padding: '6px 10px 6px 12px',
  background: t.bgInput,
  color: t.text,
  border: `1px solid ${t.borderStrong}`,
  borderRadius: t.rMd,
  fontSize: t.textMd,
  fontWeight: '400',
  lineHeight: '1.4',
  textAlign: 'left',
  whiteSpace: 'nowrap',
  cursor: 'default',
  transition: `border-color ${t.fast} ${t.ease}, box-shadow ${t.fast} ${t.ease}, background-color ${t.fast} ${t.ease}`,
  '&:hover': { background: t.bgInput, borderColor: t.textFaint },
  '&:focus-visible': { borderColor: t.accent, boxShadow: `0 0 0 3px ${t.accentSoft}`, outline: '2px solid transparent' },
  '&[aria-expanded="true"]': { borderColor: t.accent, boxShadow: `0 0 0 3px ${t.accentSoft}` },
  '&:disabled': { opacity: '0.55', cursor: 'not-allowed', background: t.bgInput },
  '& > .value': { flex: '1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis' },
  '& > .value[data-placeholder="true"]': { color: t.textFaint },
  '& > .value > .hint': { color: t.textMuted, fontSize: t.textSm, marginLeft: '6px' },
  '& > .chevron': { display: 'inline-flex', flex: 'none', color: t.textMuted, transition: `transform ${t.fast} ${t.ease}` },
  '&[aria-expanded="true"] > .chevron': { transform: 'rotate(180deg)' },
  // a label that wraps the dropdown in a toolbar ("Ask [agent] [Recommend]") keeps its words and the dropdown on one line, so
  // the buttons beside it line up with the dropdown instead of with the pair (the owner's report of 2026-10-20). A Field's
  // label names its control with for= and stays above it.
  ':where(label:not([for]):has(> &))': { display: 'inline-flex', alignItems: 'center', gap: t.s2, minWidth: '0' },
});

const smallCss = css({ minHeight: '28px', padding: '3px 8px 3px 10px', fontSize: t.textSm });

const surfaceCss = css({
  margin: '0',
  padding: '4px',
  maxHeight: 'min(320px, calc(100vh - 32px))',
  maxWidth: 'min(480px, calc(100vw - 24px))',
  overflow: 'auto',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgOverlay,
  color: t.text,
  boxShadow: t.shadowMd,
  '&:popover-open': { display: 'block' },
});

const listCss = css({ display: 'grid', gap: '1px', outline: 'none' });

const optionCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  minHeight: '30px',
  padding: `4px ${t.s2}`,
  borderRadius: t.rMd,
  fontSize: t.textMd,
  color: t.text,
  cursor: 'default',
  userSelect: 'none',
  outline: 'none',
  '& > .label': { flex: '1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& > .hint': { color: t.textMuted, fontSize: t.textSm, whiteSpace: 'nowrap' },
  '& > .check': { display: 'inline-flex', flex: 'none', width: '14px', color: t.textMuted, visibility: 'hidden' },
  '&[aria-selected="true"] > .check': { visibility: 'visible' },
  '&[aria-selected="true"] > .label': { fontWeight: '500' },
  '&[data-highlighted="true"]': { background: t.bgActive },
  '&[aria-disabled="true"]': { color: t.textFaint },
  '&[aria-disabled="true"] > .hint': { color: t.textFaint },
});

const emptyCss = css({ padding: `6px ${t.s2}`, fontSize: t.textSm, color: t.textMuted });

const TYPEAHEAD_MS = 750;

/**
 * A single-choice dropdown (`role="combobox"` on the button, a `listbox` of `option`s in an anchored popover). Keyboard:
 * Arrow keys, Home and End move, Enter and Space choose, Escape closes, letters jump to the option that starts with them
 * (and, while closed, choose it as the native control does). `onChange` fires only for a different, enabled option.
 * The options are in the page while the list is closed (hidden), so a test can click one without opening it.
 */
export function Select(handle: Handle<SelectProps>) {
  const listId = `sel-${handle.id}`;
  let open = false;
  let active = -1; // index into options of the highlighted one
  let trigger: HTMLButtonElement | undefined;
  let surface: HTMLElement | undefined;
  let list: HTMLElement | undefined;
  let typed = '';
  let typedTimer: ReturnType<typeof setTimeout> | undefined;
  handle.signal.addEventListener('abort', () => clearTimeout(typedTimer));

  const opts = () => handle.props.options;
  const selectedIndex = () => opts().findIndex((o) => o.value === handle.props.value);
  const enabled = (i: number) => i >= 0 && i < opts().length && !opts()[i]!.disabled;

  const show = () => {
    if (open || handle.props.disabled) return;
    open = true;
    typed = '';
    const sel = selectedIndex();
    active = enabled(sel) ? sel : opts().findIndex((o) => !o.disabled);
    void handle.update();
  };
  const hide = () => {
    if (!open) return;
    open = false;
    typed = '';
    void handle.update();
  };
  const highlight = (i: number) => {
    if (i === active) return;
    active = i;
    void handle.update();
    handle.queueTask(() => list?.querySelector<HTMLElement>('[data-highlighted="true"]')?.scrollIntoView({ block: 'nearest' }));
  };
  const step = (dir: 1 | -1 | 'first' | 'last') => {
    const idx: number[] = [];
    opts().forEach((x, i) => (x.disabled ? null : idx.push(i)));
    if (idx.length === 0) return;
    if (dir === 'first') return highlight(idx[0]!);
    if (dir === 'last') return highlight(idx[idx.length - 1]!);
    const at = idx.indexOf(active);
    if (at === -1) return highlight(dir === 1 ? idx[0]! : idx[idx.length - 1]!);
    const next = idx[at + dir];
    if (next !== undefined) highlight(next);
  };
  const commit = (i: number) => {
    const o = opts()[i];
    hide();
    if (!o || o.disabled) return;
    if (o.value !== handle.props.value) handle.props.onChange(o.value, o);
  };
  /** The option after `from` whose label starts with `text` (wrapping), or -1. */
  const match = (text: string, from: number) => {
    const o = opts();
    const q = text.toLowerCase();
    for (let k = 1; k <= o.length; k++) {
      const i = (from + k) % o.length;
      const x = o[i]!;
      if (!x.disabled && x.label.toLowerCase().startsWith(q)) return i;
    }
    return -1;
  };
  /** Letters typed within 750ms of each other make one search; the same letter again cycles through its matches. */
  const typeahead = (e: KeyboardEvent, whenFound: (i: number) => void) => {
    if (e.key.length !== 1 || e.ctrlKey || e.altKey || e.metaKey || (e.key === ' ' && typed === '')) return;
    clearTimeout(typedTimer);
    const key = e.key.toLowerCase();
    const current = open ? active : selectedIndex();
    const cycling = typed.length === 1 && typed === key;
    typed = cycling ? typed : typed + key;
    typedTimer = setTimeout(() => (typed = ''), TYPEAHEAD_MS);
    // a longer search starts from the current option itself (it may still match); a new or cycling one from the next
    const i = match(typed, cycling || typed.length === 1 ? current : current - 1);
    if (i !== -1) whenFound(i);
    e.preventDefault();
  };

  const onTriggerKey = (e: KeyboardEvent) => {
    if (handle.props.disabled) return;
    switch (e.key) {
      case 'ArrowDown':
      case 'ArrowUp':
      case 'Home':
      case 'End':
        e.preventDefault();
        if (!open) {
          show();
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp') return; // opened on the current option
        }
        if (e.key === 'ArrowDown') step(1);
        else if (e.key === 'ArrowUp') step(-1);
        else step(e.key === 'Home' ? 'first' : 'last');
        return;
      case 'Enter':
      case ' ':
        if (!open) {
          e.preventDefault();
          show();
        }
        return;
      case 'Escape':
        if (open) {
          e.preventDefault();
          hide();
        }
        return;
      default:
        // while closed, typing chooses the match outright, as the native control does
        if (!open) typeahead(e, (i) => commit(i));
    }
  };

  const onListKey = (e: KeyboardEvent) => {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        return step(1);
      case 'ArrowUp':
        e.preventDefault();
        return step(-1);
      case 'Home':
        e.preventDefault();
        return step('first');
      case 'End':
        e.preventDefault();
        return step('last');
      case 'Enter':
      case ' ':
        e.preventDefault();
        if (active !== -1) commit(active);
        else hide();
        return;
      case 'Tab':
        e.preventDefault();
        return hide();
      default:
        typeahead(e, (i) => highlight(i));
    }
  };

  return () => {
    const { id, value, options, disabled, label, placeholder, testId, size = 'md', title, name, style, mix } = handle.props;
    if (disabled && open) open = false;
    const sel = options.find((o) => o.value === value);
    const shown = sel ? sel.label : value || placeholder || '';
    return (
      <popover.Context>
        <button
          type="button"
          id={id}
          role="combobox"
          aria-haspopup="listbox"
          aria-expanded={open ? 'true' : 'false'}
          aria-controls={listId}
          aria-activedescendant={open && active !== -1 ? `${listId}-${active}` : undefined}
          aria-label={label}
          title={title ?? (typeof shown === 'string' && shown ? shown : undefined)}
          disabled={disabled}
          data-testid={testId}
          data-value={value}
          style={style}
          mix={[
            triggerCss,
            size === 'sm' ? smallCss : undefined,
            ref((n) => (trigger = n as HTMLButtonElement)),
            popover.anchor({ placement: 'bottom-start', offset: 4 }),
            popover.focusOnHide(),
            on('click', () => (open ? hide() : show())),
            on('keydown', (e) => onTriggerKey(e as KeyboardEvent)),
            ...(mix ?? []),
          ]}
        >
          <span class="value" data-placeholder={sel || value ? undefined : 'true'}>
            {shown}
            {sel?.hint ? <span class="hint">{sel.hint}</span> : null}
          </span>
          <span class="chevron" aria-hidden="true">
            <Icon icon={ChevronDown} size={15} />
          </span>
        </button>
        {name ? <input type="hidden" name={name} value={value} /> : null}
        <div
          mix={[
            surfaceCss,
            ref((n) => (surface = n as HTMLElement)),
            popover.surface({ open, onHide: () => hide() }),
            on('beforetoggle', (e) => {
              // as wide as the control it opens from, before it is placed
              if ((e as ToggleEvent).newState === 'open' && surface && trigger) surface.style.minWidth = `${trigger.offsetWidth}px`;
            }),
            on('toggle', (e) => {
              if ((e as ToggleEvent).newState === 'open') list?.querySelector<HTMLElement>('[data-highlighted="true"]')?.scrollIntoView({ block: 'nearest' });
            }),
          ]}
        >
          <div
            id={listId}
            role="listbox"
            tabIndex={-1}
            aria-activedescendant={open && active !== -1 ? `${listId}-${active}` : undefined}
            mix={[listCss, ref((n) => (list = n as HTMLElement)), popover.focusOnShow(), on('keydown', (e) => onListKey(e as KeyboardEvent))]}
          >
            {options.length === 0 ? <div mix={emptyCss}>Nothing to choose from.</div> : null}
            {options.map((o, i) => (
              <div
                key={o.value}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={o.value === value ? 'true' : 'false'}
                aria-disabled={o.disabled ? 'true' : undefined}
                data-highlighted={open && i === active ? 'true' : 'false'}
                data-value={o.value}
                mix={[
                  optionCss,
                  on('click', (e) => {
                    e.preventDefault();
                    commit(i);
                  }),
                  on('mousemove', () => {
                    if (!o.disabled) highlight(i);
                  }),
                ]}
              >
                <span class="check" aria-hidden="true">
                  <Icon icon={Check} size={14} />
                </span>
                <span class="label">{o.label}</span>
                {o.hint ? <span class="hint">{o.hint}</span> : null}
              </div>
            ))}
          </div>
        </div>
      </popover.Context>
    );
  };
}

/** Options from plain `[value, label]` pairs. */
export const selectOptions = (pairs: readonly (readonly [string, string])[]): SelectOption[] => pairs.map(([value, label]) => ({ value, label }));

/** Options from a list of strings, each its own label. */
export const selectValues = (values: readonly string[]): SelectOption[] => values.map((v) => ({ value: v, label: v }));
