import { css, on, ref, type Handle, type RemixNode } from 'remix/component';
import { X } from 'lucide';
import { IconButton } from './button.tsx';
import { t } from './tokens.ts';

const dialogCss = css({
  width: 'min(560px, calc(100vw - 32px))',
  maxHeight: 'calc(100vh - 64px)',
  padding: '0',
  border: '0',
  borderRadius: t.rXl,
  background: t.bgOverlay,
  color: t.text,
  boxShadow: t.shadowLg,
  overflow: 'hidden',
  '&::backdrop': { background: t.scrim, backdropFilter: 'blur(2px)' },
  '&[open]': { display: 'flex', flexDirection: 'column', animation: `dd-pop ${t.med} ${t.ease}` },
  '@keyframes dd-pop': { from: { opacity: '0', transform: 'translateY(6px) scale(0.98)' }, to: { opacity: '1', transform: 'none' } },
});
const wideDialog = css({ width: 'min(880px, calc(100vw - 32px))' });
const headCss = css({
  display: 'flex',
  alignItems: 'center',
  gap: t.s2,
  padding: `${t.s3} ${t.s3} ${t.s3} ${t.s5}`,
  borderBottom: `1px solid ${t.border}`,
  '& h2': { flex: '1', fontSize: t.textLg },
});
const bodyCss = css({ padding: t.s5, overflow: 'auto', display: 'grid', gap: t.s4 });

/**
 * A modal dialog on the native <dialog> (focus trap, Escape and the top layer come from the browser). Controlled: render
 * it with open and handle onClose (Escape, the close button, a click on the backdrop). The dialog is named by its heading
 * (aria-labelledby), or by `label` when one is given.
 */
export function Dialog(handle: Handle<{ open: boolean; title: RemixNode; onClose: () => void; wide?: boolean; children?: RemixNode; label?: string }>) {
  let node: HTMLDialogElement | undefined;
  const titleId = `dlg-${handle.id}-title`;
  const sync = () => {
    if (!node) return;
    if (handle.props.open && !node.open) node.showModal();
    else if (!handle.props.open && node.open) node.close();
  };
  return () => {
    handle.queueTask(sync);
    const { title, onClose, wide, children, label } = handle.props;
    return (
      <dialog
        aria-label={label}
        aria-labelledby={label ? undefined : titleId}
        mix={[
          dialogCss,
          ref((n) => {
            node = n;
            sync();
          }),
          on('cancel', (e) => {
            e.preventDefault();
            onClose();
          }),
          on('click', (e) => {
            if (e.target === e.currentTarget) onClose();
          }),
          wide ? wideDialog : undefined,
        ]}
      >
        {handle.props.open ? (
          <>
            <div mix={headCss}>
              <h2 id={titleId}>{title}</h2>
              <IconButton icon={X} label="Close" mix={on('click', () => onClose())} />
            </div>
            <div mix={bodyCss}>{children}</div>
          </>
        ) : null}
      </dialog>
    );
  };
}
