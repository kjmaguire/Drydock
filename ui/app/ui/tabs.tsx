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
 * `onChange`. children(name) renders the active panel's content.
 */
export function Tabs(
  handle: Handle<{ label: string; items: TabItem[]; active: string; onChange: (name: string) => void; children?: (name: string) => RemixNode; panelMix?: ReturnType<typeof css> }>,
) {
  return () => {
    const { label, items, active, onChange, children, panelMix } = handle.props;
    return (
      <tabs.Context activeTab={active} onActiveTabChange={onChange}>
        <div mix={tabs.root()}>
          <div aria-label={label} mix={[listCss, tabs.list()]}>
            {items.map((it) => (
              <button key={it.name} type="button" data-testid={it.testId} mix={[tabCss, tabs.tab({ name: it.name })]}>
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
