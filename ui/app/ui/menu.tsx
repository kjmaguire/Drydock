import { css, type Handle, type RemixNode } from 'remix/component';
import * as menu from '@remix-run/ui/menu';
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
}

/**
 * A dropdown menu on @remix-run/ui menu (keyboard, typeahead, focus return). `trigger` renders the opener and receives the
 * mixin to put on it.
 */
export function Menu(
  handle: Handle<{ label: string; items: MenuItem[]; onSelect: (name: string) => void; trigger: (mix: ReturnType<typeof menu.trigger>) => RemixNode; placement?: 'bottom-start' | 'bottom-end' | 'right-start' }>,
) {
  return () => {
    const { label, items, onSelect, trigger, placement = 'bottom-start' } = handle.props;
    return (
      <div style={{ display: 'contents' }} mix={menu.onMenuSelect((e) => onSelect(e.item.name))}>
        <menu.Context label={label}>
          {trigger(menu.trigger({ placement, offset: 4 }))}
          <div mix={[surfaceCss, menu.popover()]}>
            <div mix={[listCss, menu.list()]}>
              {items.map((it) => [
                it.separatorBefore ? <div key={`${it.name}-sep`} role="separator" mix={sepCss} /> : null,
                <div key={it.name} data-tone={it.danger ? 'danger' : undefined} mix={[itemCss, menu.item({ name: it.name, label: it.label, disabled: it.disabled })]}>
                  {it.icon ? <Icon icon={it.icon} size={15} /> : null}
                  {it.label}
                  {it.hint ? <kbd>{it.hint}</kbd> : null}
                </div>,
              ])}
            </div>
          </div>
        </menu.Context>
      </div>
    );
  };
}
