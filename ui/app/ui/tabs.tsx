import { css, type Handle, type RemixNode } from 'remix/component';
import * as tabs from '@remix-run/ui/tabs';
import { t } from './tokens.ts';

export interface TabItem {
  name: string;
  label: RemixNode;
  count?: number;
  testId?: string;
}

const listCss = css({
  display: 'flex',
  alignItems: 'stretch',
  gap: t.s1,
  borderBottom: `1px solid ${t.border}`,
  overflowX: 'auto',
  scrollbarWidth: 'none',
});

const tabCss = css({
  position: 'relative',
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  minHeight: '36px',
  padding: `0 ${t.s3}`,
  border: '0',
  borderRadius: '0',
  background: 'transparent',
  color: t.textMuted,
  fontSize: t.textMd,
  fontWeight: '500',
  whiteSpace: 'nowrap',
  '&:hover': { background: 'transparent', color: t.text },
  '&[data-state="active"]': { color: t.text },
  '&[data-state="active"]::after': {
    content: '""',
    position: 'absolute',
    left: t.s2,
    right: t.s2,
    bottom: '-1px',
    height: '2px',
    borderRadius: '2px 2px 0 0',
    background: t.accent,
  },
  '&:focus-visible': { boxShadow: `inset ${t.focus}` },
});

// the pill variant (Crew HQ): a row of rounded pills, the active one a white card
const pillListCss = css({ display: 'flex', alignItems: 'center', gap: t.s1, borderBottom: '0', overflowX: 'auto', scrollbarWidth: 'none' });
const pillCss = css({
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  height: '36px',
  padding: `0 ${t.s4}`,
  border: '1px solid transparent',
  borderRadius: t.rFull,
  background: 'transparent',
  color: t.textMuted,
  fontSize: t.textMd,
  fontWeight: '500',
  whiteSpace: 'nowrap',
  '&:hover': { background: t.bgHover, color: t.text },
  '&[data-state="active"]': { background: t.bgSurface, color: t.text, borderColor: t.border, boxShadow: t.shadowSm },
  '&:focus-visible': { boxShadow: t.focus, outline: 'none' },
});

const countCss = css({
  minWidth: '18px',
  height: '16px',
  padding: '0 5px',
  borderRadius: t.rFull,
  background: t.bgActive,
  color: t.textMuted,
  fontSize: t.textXs,
  lineHeight: '16px',
  textAlign: 'center',
});

/**
 * Tabs on the headless @remix-run/ui tabs primitive (roving focus, arrow keys, ARIA). Controlled: `active` and
 * `onChange`. children(name) renders the active panel's content; every panel element is rendered (the inactive ones
 * hidden and empty), so each tab's aria-controls names an element that exists. Without children the tabs name no panel.
 */
export function Tabs(
  handle: Handle<{
    label: string;
    items: TabItem[];
    active: string;
    onChange: (name: string) => void;
    children?: (name: string) => RemixNode;
    panelMix?: ReturnType<typeof css>;
    /** Pills (a page's sections, Crew HQ) instead of the underlined tabs of a panel. */
    pills?: boolean;
  }>,
) {
  return () => {
    const { label, items, active, onChange, children, panelMix, pills } = handle.props;
    return (
      <tabs.Context activeTab={active} onActiveTabChange={onChange}>
        <div mix={tabs.root()}>
          <div aria-label={label} mix={[pills ? pillListCss : listCss, tabs.list()]}>
            {items.map((it) => (
              <button
                key={it.name}
                type="button"
                data-testid={it.testId}
                // without children the caller renders the content itself: there is no panel for aria-controls to name (an
                // explicit null overrides the primitive's default, which would point at an element that does not exist)
                aria-controls={children ? undefined : (null as unknown as string)}
                mix={[pills ? pillCss : tabCss, tabs.tab({ name: it.name })]}
              >
                {it.label}
                {it.count ? <span mix={countCss}>{it.count}</span> : null}
              </button>
            ))}
          </div>
          {children
            ? items.map((it) => (
                <div key={it.name} mix={[tabs.panel({ name: it.name }), panelMix]}>
                  {it.name === active ? children(it.name) : null}
                </div>
              ))
            : null}
        </div>
      </tabs.Context>
    );
  };
}
