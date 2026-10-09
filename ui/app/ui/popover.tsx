import { css, on, type Handle, type MixInput, type RemixNode } from 'remix/component';
import * as popover from '@remix-run/ui/popover';
import { t } from './tokens.ts';

export const popoverSurface = css({
  margin: '0',
  width: 'min(340px, calc(100vw - 24px))',
  maxHeight: 'min(480px, calc(100vh - 48px))',
  overflow: 'auto',
  padding: t.s3,
  border: `1px solid ${t.border}`,
  borderRadius: t.rLg,
  background: t.bgOverlay,
  color: t.text,
  boxShadow: t.shadowMd,
  '&:popover-open': { display: 'grid', gap: t.s3 },
  '& h2': { margin: '0', fontSize: t.textMd, fontWeight: '600' },
});

/**
 * An anchored, dismissible panel on @remix-run/ui popover (Escape and an outside click close it, focus returns to the
 * opener). Controlled: render it with open and handle onClose. `trigger` renders the opener and receives the mixins to put
 * on it (anchor, focus return, the click that opens).
 */
export function Popover(
  handle: Handle<{
    open: boolean;
    onOpen: () => void;
    onClose: () => void;
    label: string;
    trigger: (mix: MixInput<HTMLElement>) => RemixNode;
    placement?: 'bottom-start' | 'bottom-end' | 'top-start' | 'top-end';
    children?: RemixNode;
    testId?: string;
    /** false: a click outside closes the panel and still reaches what was clicked (default true: it only closes). */
    stopOutsideClick?: boolean;
  }>,
) {
  return () => {
    const { open, onOpen, onClose, label, trigger, placement = 'bottom-start', children, testId, stopOutsideClick = true } = handle.props;
    return (
      <popover.Context>
        {trigger([popover.anchor({ placement, offset: 4 }), popover.focusOnHide(), on('click', () => (open ? onClose() : onOpen()))])}
        <div role="dialog" aria-label={label} data-testid={testId} mix={[popoverSurface, popover.surface({ open, onHide: () => onClose(), stopOutsideClickPropagation: stopOutsideClick })]}>
          {open ? children : null}
        </div>
      </popover.Context>
    );
  };
}
