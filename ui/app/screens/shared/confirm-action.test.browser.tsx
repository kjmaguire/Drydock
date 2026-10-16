import { expect } from 'remix/assert';
import { on, type Handle } from 'remix/component';
import { afterEach, describe, it } from 'remix/test';
import { byText, cleanup, drain, renderPlain, waitFor } from '../../test/render.tsx';
import { ConfirmAction } from './confirm-action.tsx';

afterEach(cleanup);

// audit 2026-10-16, L40: an action that cannot be undone asks first, and only "Yes" does it.

const names = (root: ParentNode) => Array.from(root.querySelectorAll('button')).map((b) => (b.getAttribute('aria-label') ?? b.textContent ?? '').trim());
const QUESTION = 'Delete the rule "r1"? It cannot be brought back.';

function show(onConfirm: () => unknown, disabled = false) {
  return renderPlain(
    <ConfirmAction
      label="Delete"
      yes="Yes, delete"
      yesName={'Yes, delete the rule "r1"'}
      keepName={'Keep the rule "r1"'}
      question={QUESTION}
      disabled={disabled}
      onConfirm={onConfirm}
      testId="del"
    />,
  );
}

describe('ConfirmAction', () => {
  it('one click asks and does nothing; Keep goes back and puts focus on the first button again', async () => {
    let calls = 0;
    const r = show(() => void calls++);
    expect(names(r.container)).toEqual(['Delete']);
    await r.act(() => byText(r.container, 'button', 'Delete').click());
    expect(calls).toBe(0);
    // the buttons say what they do, and the pair is named by the question in a sentence
    expect(r.$('[role="group"]')!.getAttribute('aria-label')).toBe(QUESTION);
    expect(names(r.container)).toEqual(['Yes, delete the rule "r1"', 'Keep the rule "r1"']);
    expect(byText(r.container, 'button', 'Yes, delete the rule "r1"').getAttribute('data-testid')).toBe('del-confirm');
    await r.act(() => byText(r.container, 'button', 'Keep the rule "r1"').click());
    await waitFor(() => expect(names(r.container)).toEqual(['Delete']), r);
    expect(calls).toBe(0);
    await waitFor(() => expect(document.activeElement).toBe(r.$('[data-testid="del"]')), r);
  });

  it('moves focus to Keep when the question opens, so a stray Enter keeps the thing', async () => {
    let calls = 0;
    const r = show(() => void calls++);
    await r.act(() => byText(r.container, 'button', 'Delete').click());
    await waitFor(() => expect(document.activeElement).toBe(byText(r.container, 'button', 'Keep the rule "r1"')), r);
    expect(calls).toBe(0);
  });

  it('Yes does it once, even when it is clicked twice before the action has ended, and then the question closes', async () => {
    let calls = 0;
    let finish: () => void = () => undefined;
    const r = show(
      () =>
        new Promise<void>((res) => {
          calls++;
          finish = res;
        }),
    );
    await r.act(() => byText(r.container, 'button', 'Delete').click());
    const yes = byText<HTMLButtonElement>(r.container, 'button', 'Yes, delete the rule "r1"');
    await r.act(() => {
      yes.click();
      yes.click();
    });
    await drain();
    expect(calls).toBe(1);
    // while it runs neither button can be pressed again
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Yes, delete the rule "r1"').disabled).toBe(true);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Keep the rule "r1"').disabled).toBe(true);
    finish();
    await waitFor(() => expect(names(r.container)).toEqual(['Delete']), r);
    expect(calls).toBe(1);
  });

  it('an action that throws closes the question and leaves the screen to show the failure', async () => {
    const r = show(async () => {
      throw new Error('the core refused');
    });
    await r.act(() => byText(r.container, 'button', 'Delete').click());
    await r.act(() => byText(r.container, 'button', 'Yes, delete the rule "r1"').click());
    await waitFor(() => expect(names(r.container)).toEqual(['Delete']), r);
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Delete').disabled).toBe(false);
  });

  it('a disabled first button cannot ask, and Yes goes off when the screen becomes busy with something else', async () => {
    let calls = 0;
    const off = show(() => void calls++, true);
    expect(byText<HTMLButtonElement>(off.container, 'button', 'Delete').disabled).toBe(true);
    cleanup();

    function Host(handle: Handle) {
      let busy = false;
      return () => (
        <div>
          <button type="button" data-testid="busy" mix={on('click', () => ((busy = !busy), void handle.update()))}>
            busy
          </button>
          <ConfirmAction label="Delete" yes="Yes, delete" question={QUESTION} disabled={busy} onConfirm={() => void calls++} />
        </div>
      );
    }
    const r = renderPlain(<Host />);
    await r.act(() => byText(r.container, 'button', 'Delete').click());
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Yes, delete').disabled).toBe(false);
    await r.act(() => (r.$('[data-testid="busy"]') as HTMLButtonElement).click());
    expect(byText<HTMLButtonElement>(r.container, 'button', 'Yes, delete').disabled).toBe(true);
    await r.act(() => byText<HTMLButtonElement>(r.container, 'button', 'Yes, delete').click());
    expect(calls).toBe(0);
  });
});
