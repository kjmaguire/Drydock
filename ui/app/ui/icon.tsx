// Icons are Lucide icon nodes (ISC, bundled, tree-shaken: import only the icons you use from 'lucide') drawn as inline SVG
// in the text color. Decorative unless a label is given.

import { createElement, type Handle } from 'remix/component';
import type { IconNode } from 'lucide';

export type { IconNode };

export function Icon(handle: Handle<{ icon: IconNode; size?: number; label?: string; strokeWidth?: number }>) {
  return () => {
    const { icon, size = 16, label, strokeWidth = 1.75 } = handle.props;
    return (
      <svg
        xmlns="http://www.w3.org/2000/svg"
        width={size}
        height={size}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width={String(strokeWidth)}
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden={label ? undefined : 'true'}
        aria-label={label}
        role={label ? 'img' : undefined}
        focusable="false"
        style={{ flex: 'none' }}
      >
        {icon.map(([tag, attrs], i) => createElement(tag, { ...attrs, key: i }))}
      </svg>
    );
  };
}
