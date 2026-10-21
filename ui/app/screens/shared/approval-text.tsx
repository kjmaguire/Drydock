// What a person is shown before they approve something an agent asked for: where the call goes, a note when part of the input is
// not in front of them, and a note when the text holds characters that are not drawn (audit 2026-10-16, H8). The input itself is
// agent output: it is shown as text, with `visible` writing out the characters that reorder or hide text.

import { css, type Handle } from 'remix/component';
import { hasHidden, visible } from '../../lib/visible.ts';
import { Notice } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';

const targetsCss = css({
  display: 'grid',
  gridTemplateColumns: 'max-content 1fr',
  gap: `4px ${t.s3}`,
  margin: '0',
  fontSize: t.textSm,
  '& dt': { color: t.textMuted },
  '& dd': { margin: '0', minWidth: '0', overflowWrap: 'anywhere' },
  '& code': { fontFamily: t.mono },
});

/** The paths and addresses a call names, each whole, so the person reads where it goes before the body of the call. */
export function Targets(handle: Handle<{ targets?: readonly string[]; testId?: string }>) {
  return () => {
    const { targets, testId = 'approval-targets' } = handle.props;
    if (!targets || targets.length === 0) return null;
    return (
      <dl mix={targetsCss}>
        <dt>{targets.length === 1 ? 'Where it goes' : 'Where it goes (each)'}</dt>
        <dd data-testid={testId}>
          {targets.map((x) => (
            <div key={x}>
              <code>{visible(x)}</code>
            </div>
          ))}
        </dd>
      </dl>
    );
  };
}

/**
 * The two notes that belong above an input: that part of it is cut (allowing still covers all of it), and that it holds
 * characters which are written out instead of drawn. `texts` are the strings shown below the notes.
 */
export function ShownNotes(handle: Handle<{ shortened?: boolean; texts: readonly (string | undefined)[]; testId?: string }>) {
  return () => {
    const { shortened, texts, testId = 'approval' } = handle.props;
    const hidden = texts.some((x) => hasHidden(x));
    if (!shortened && !hidden) return null;
    return (
      <>
        {shortened ? (
          <Notice tone="warn" testId={`${testId}-shortened`} title="Part of this input is not shown.">
            A long value below is cut where it says so. Approving covers the whole call, including the part you cannot see. Refuse it if that is not what
            you want to allow.
          </Notice>
        ) : null}
        {hidden ? (
          <Notice tone="warn" testId={`${testId}-hidden`} title="This text has characters that are not drawn.">
            Characters that reorder or hide text are written out as <code>{'\\u{...}'}</code> below, so the text is shown as it is.
          </Notice>
        ) : null}
      </>
    );
  };
}
