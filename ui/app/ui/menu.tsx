import { css, type Handle, type RemixNode } from 'remix/component';
import * as menu from '@remix-run/ui/menu';
import { Check } from 'lucide';
import { Icon, type IconNode } from './icon.tsx';
import { t } from './tokens.ts';

export const surfaceCss = css({
  margin: '0',
  minWidth: '200px',
  padding: '4px',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgOverlay,
  color: t.text,
  boxShadow: t.shadowMd,
  '&:popover-open': { display: 'block' },
});

const listCss = css({ display: 'grid', gap: '1px', outline: 'none' });

const itemCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  height: '30px',
  padding: `0 ${t.s2}`,
  borderRadius: t.rMd,
  fontSize: t.textMd,
  color: t.text,
  cursor: 'default',
  userSelect: 'none',
  outline: 'none',
  '& svg': { color: t.textMuted },
  '&[data-highlighted="true"]': { background: t.bgActive },
  '&[aria-disabled="true"]': { color: t.textFaint },
  '&[data-tone="danger"]': { color: t.danger },
  '&[data-tone="danger"] svg': { color: t.danger },
  '& kbd': { marginLeft: 'auto' },
  '& > .label': { flex: '1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  '& > .check': { display: 'inline-flex', marginLeft: 'auto', color: t.textMuted, visibility: 'hidden' },
  '&[aria-checked="true"] > .check': { visibility: 'visible' },
});

const sepCss = css({ height: '1px', margin: '4px 6px', background: t.border });

export interface MenuItem {
  name: string;
  label: string;
  icon?: IconNode;
  hint?: string;
  danger?: boolean;
  disabled?: boolean;
  separatorBefore?: boolean;
  /** A radio (one of a set, checked = the current one) or checkbox item. */
  type?: 'radio' | 'checkbox';
  checked?: boolean;
  /** Drawn before the label instead of the icon (e.g. a monogram). Decorative. */
  lead?: RemixNode;
  /** data-value on the item, for tests and styling. */
  value?: string;
}

/**
 * A dropdown menu on @remix-run/ui menu (keyboard, typeahead, focus return). `trigger` renders the opener and receives the
 * mixin to put on it.
 */
export function Menu(
  handle: Handle<{ label: string; items: MenuItem[]; onSelect: (name: string) => void; trigger: (mix: ReturnType<typeof menu.trigger>) => RemixNode; placement?: 'bottom-start' | 'bottom-end' | 'right-start'; surface?: ReturnType<typeof css> }>,
) {
  return () => {
    const { label, items, onSelect, trigger, placement = 'bottom-start', surface } = handle.props;
    return (
      <div style={{ display: 'contents' }} mix={menu.onMenuSelect((e) => onSelect(e.item.name))}>
        <menu.Context label={label}>
          {trigger(menu.trigger({ placement, offset: 4 }))}
          <div mix={[surfaceCss, menu.popover(), surface]}>
            <div mix={[listCss, menu.list()]}>
              {items.map((it) => [
                it.separatorBefore ? <div key={`${it.name}-sep`} role="separator" mix={sepCss} /> : null,
                <div
                  key={it.name}
                  data-tone={it.danger ? 'danger' : undefined}
                  data-value={it.value}
                  mix={[itemCss, menu.item({ name: it.name, label: it.label, disabled: it.disabled, type: it.type, checked: it.checked, value: it.value })]}
                >
                  {it.lead ?? (it.icon ? <Icon icon={it.icon} size={15} /> : null)}
                  <span class="label">{it.label}</span>
                  {it.hint ? <kbd>{it.hint}</kbd> : null}
                  {it.type ? (
                    <span class="check" aria-hidden="true">
                      <Icon icon={Check} size={14} />
                    </span>
                  ) : null}
                </div>,
              ])}
            </div>
          </div>
        </menu.Context>
      </div>
    );
  };
}
