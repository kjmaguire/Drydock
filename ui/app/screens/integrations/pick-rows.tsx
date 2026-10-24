// The compact rows of a project's Integrations page (decision P98): a service or a server per row (its logo, its name over one
// line about what this project has of it, its state in a word at the end) on one surface, the chosen row pressed; its panel opens
// under the rows. The same row as the workspace page's list (ui/master-detail.tsx), without the list's own layout.

import { css, on, type Handle } from 'remix/component';
import type { IconNode } from '../../ui/icon.tsx';
import { ItemMark, rowCss, StatusWord } from '../../ui/master-detail.tsx';
import { t, type Tone } from '../../ui/tokens.ts';

const listCss = css({
  display: 'grid',
  margin: '0',
  padding: '4px',
  listStyle: 'none',
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgSurface,
  minWidth: '0',
});
// after the row's own rule, so the pressed row's background wins (each css() rule is its own cascade layer)
const pressedCss = css({ '&[aria-pressed="true"]': { background: t.bgActive } });

export interface PickRow {
  key: string;
  title: string;
  logo?: string;
  icon?: IconNode;
  sub?: string;
  status: { text: string; tone: Tone | 'none' };
  testId: string;
  data?: Record<string, string>;
}

/** The rows; `selected` is the pressed one, `onPick` presses another (or the same one again, to close its panel). */
export function PickRows(handle: Handle<{ label: string; rows: PickRow[]; selected?: string; onPick: (key: string) => void }>) {
  return () => {
    const { label, rows, selected, onPick } = handle.props;
    return (
      <ul mix={listCss} aria-label={label}>
        {rows.map((r) => {
          const attrs: Record<string, string> = {};
          for (const [k, v] of Object.entries(r.data ?? {})) attrs[`data-${k}`] = v;
          return (
            <li key={r.key}>
              <button type="button" mix={[rowCss, pressedCss, on('click', () => onPick(r.key))]} aria-pressed={r.key === selected ? 'true' : 'false'} data-testid={r.testId} {...attrs}>
                <ItemMark logo={r.logo} icon={r.icon} />
                <span data-part="text">
                  <span data-part="title">{r.title}</span>
                  {r.sub ? <span data-part="sub">{r.sub}</span> : null}
                </span>
                <StatusWord text={r.status.text} tone={r.status.tone} testId="row-status" />
              </button>
            </li>
          );
        })}
      </ul>
    );
  };
}
