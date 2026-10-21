// A destructive action in two steps (audit 2026-10-16, L40): the button asks first, and only "Yes, <verb>" does it. It is the
// inline confirm the budgets, providers and library screens already have (the button becomes "Yes, <verb>" in the danger
// variant beside "Keep"), written once for the rows that were one click: while it asks nothing is sent; "Yes" is turned off
// from its first click until the action has ended, so a second click cannot send it twice; focus moves to "Keep" when the
// question opens (a stray Enter keeps the thing) and back to the first button when it closes without the thing being gone.
// The action reports its own failure (act.run shows it where the screen shows errors); this only closes the question.

import { css, on, ref, type Handle } from 'remix/component';
import { Button, type ButtonSize, type ButtonVariant } from '../../ui/button.tsx';
import type { IconNode } from '../../ui/icon.tsx';
import { t } from '../../ui/tokens.ts';

const askCss = css({ display: 'inline-flex', alignItems: 'center', flexWrap: 'wrap', gap: t.s2 });

export interface ConfirmActionProps {
  /** The first button: "Delete". */
  label: string;
  icon?: IconNode;
  /** The first button's look: the quiet one a row has by default. */
  variant?: ButtonVariant;
  size?: ButtonSize;
  /** The second button, in the danger variant: "Yes, delete". */
  yes: string;
  /** Its accessible name where several rows share the words: `Yes, delete the rule "r1"`. It starts with `yes`. */
  yesName?: string;
  /** The way back: "Keep" (default). */
  keep?: string;
  keepName?: string;
  /** What is about to happen, in a sentence: the name of the pair of buttons, read out when focus arrives (a sighted person sees the two buttons only, as in the other screens). */
  question?: string;
  /** Turned off while something else is running. */
  disabled?: boolean;
  /** Runs once, on "Yes". Its failure is shown by whoever runs it, not here. */
  onConfirm: () => unknown;
  /** data-testid of the first button (the confirm button gets `<testId>-confirm`). */
  testId?: string;
}

export function ConfirmAction(handle: Handle<ConfirmActionProps>) {
  let asking = false;
  let pending = false;
  let first: HTMLElement | undefined;

  const settled = async () => {
    asking = false;
    pending = false;
    if (handle.signal.aborted) return;
    await handle.update();
    first?.focus(); // the row is still there (the action failed or changed nothing): focus stays where the person was
  };

  return () => {
    const { label, icon, variant = 'ghost', size = 'sm', yes, yesName, keep = 'Keep', keepName, question, disabled = false, onConfirm, testId } = handle.props;
    if (!asking) {
      return (
        <Button
          size={size}
          variant={variant}
          icon={icon}
          disabled={disabled}
          data-testid={testId}
          mix={[
            ref((node) => (first = node as HTMLElement)),
            on('click', () => {
              asking = true;
              void handle.update();
            }),
          ]}
        >
          {label}
        </Button>
      );
    }
    return (
      <span mix={askCss} role="group" aria-label={question ?? yesName ?? yes}>
        <Button
          size={size}
          variant="danger"
          disabled={disabled || pending}
          aria-label={yesName}
          data-testid={testId ? `${testId}-confirm` : undefined}
          mix={on('click', async () => {
            if (pending) return;
            pending = true;
            void handle.update();
            try {
              await onConfirm();
            } catch {
              /* the caller shows its own failure */
            }
            await settled();
          })}
        >
          {yes}
        </Button>
        <Button
          size={size}
          disabled={pending}
          aria-label={keepName}
          mix={[
            ref((node) => (node as HTMLElement).focus()),
            on('click', () => {
              void settled();
            }),
          ]}
        >
          {keep}
        </Button>
      </span>
    );
  };
}
