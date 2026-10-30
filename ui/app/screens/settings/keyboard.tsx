// Keyboard shortcuts (decision P132): a person binds a shortcut of the map (lib/keys.ts SHORTCUTS) to other keys. Change waits
// for the new keys (pressed once, Esc cancels), says at once why keys cannot be used (no Ctrl or Alt, the window's own, another
// shortcut's) and sends nothing then; Save gives every binding to the core (keys.set), which checks the same and keeps them for
// the workspace. Reset puts one back to its default, Reset all every one.

import { css, on, ref, type Handle } from 'remix/component';
import { Keyboard } from 'lucide';
import { bindingProblem, bindings, comboFor, comboOf, SHORTCUTS, type ShortcutId } from '../../lib/keys.ts';
import { action, services, watch } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { t, tone } from '../../ui/tokens.ts';
import { Keys } from './about.tsx';
import { Group, panel } from './settings-kit.tsx';

const listCss = css({ listStyle: 'none', margin: '0', padding: '0' });
const rowCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: `${t.s1} ${t.s3}`,
  fontSize: t.textMd,
  '& > [data-part="what"]': { flex: '1 1 220px', minWidth: '0' },
  '& > [data-part="problem"]': { flexBasis: '100%', margin: '0', color: tone.err.fg, fontSize: t.textSm },
});
const captureCss = css({
  minHeight: '28px',
  minWidth: '120px',
  padding: `0 ${t.s2}`,
  border: `1px dashed ${t.accent}`,
  borderRadius: t.rMd,
  background: t.bgApp,
  color: t.textMuted,
  fontSize: t.textSm,
});

export function KeyboardSection(handle: Handle) {
  const s = services(handle);
  watch(handle, s.ui); // the bindings in use (s.ui.setKeys)
  watch(handle, s.workspace);
  const act = action(handle);
  let editing: ShortcutId | undefined;
  let pressed = '';
  let problem = '';
  const upd = (f: () => void) => {
    f();
    void handle.update();
  };
  const stop = () =>
    upd(() => {
      editing = undefined;
      pressed = '';
      problem = '';
    });
  const save = async (next: Partial<Record<ShortcutId, string>>) => {
    const r = await act.run('save', () => s.client.keysSet(next as Record<string, string>));
    if (!r) return;
    s.ui.setKeys(r.bindings);
    stop();
  };
  const without = (id: ShortcutId) => {
    const b = bindings();
    delete b[id];
    return b;
  };
  const capture = (id: ShortcutId, e: KeyboardEvent) => {
    if (e.key === 'Tab') return; // the keyboard may leave the field
    e.preventDefault();
    e.stopPropagation(); // the keys are being chosen: no shortcut of the window acts on them
    if (e.key === 'Escape' && !e.ctrlKey && !e.altKey && !e.metaKey) return stop();
    const c = comboOf(e);
    if (!c) return;
    upd(() => {
      pressed = c;
      problem = bindingProblem(id, c);
    });
  };

  return () => {
    const b = bindings();
    const readOnly = s.workspace.license.readOnly;
    return (
      <Group
        title="Keyboard shortcuts"
        icon={Keyboard}
        label="Keyboard shortcuts"
        testId="keys-section"
        description="Give a shortcut other keys. Your keys are kept for this workspace and every window of it."
      >
        <ul mix={[panel, listCss]} role="list" aria-label="Shortcuts you can change">
          {SHORTCUTS.map((l) => {
            const mine = !!b[l.id];
            return (
              <li key={l.id} mix={rowCss} data-testid="keys-row" data-id={l.id} data-bound={mine ? 'true' : undefined}>
                <span data-part="what">{l.action}</span>
                {editing === l.id ? (
                  <>
                    <button
                      type="button"
                      mix={[captureCss, on('keydown', (e) => capture(l.id, e)), ref((el) => (el as HTMLElement).focus())]}
                      data-testid="keys-capture"
                      aria-label={`Press the new keys for: ${l.action}`}
                    >
                      {pressed ? <Keys keys={pressed.split(/\+(?!$)/)} /> : 'Press the keys'}
                    </button>
                    <Button size="sm" variant="primary" data-testid="keys-save" disabled={!pressed || !!problem || act.busy === 'save'} mix={on('click', () => void save({ ...b, [l.id]: pressed }))}>
                      Save
                    </Button>
                    <Button size="sm" variant="ghost" data-testid="keys-cancel" mix={on('click', stop)}>
                      Cancel
                    </Button>
                    {problem ? (
                      <p data-part="problem" role="alert" data-testid="keys-problem">
                        {problem}
                      </p>
                    ) : null}
                  </>
                ) : (
                  <>
                    <Keys keys={comboFor(l.id).split(/\+(?!$)/)} />
                    <Button
                      size="sm"
                      variant="ghost"
                      data-testid="keys-change"
                      disabled={readOnly || !!editing}
                      aria-label={`Change the keys of: ${l.action}`}
                      mix={on('click', () => upd(() => ((editing = l.id), (pressed = ''), (problem = ''), act.clear())))}
                    >
                      Change
                    </Button>
                    {mine ? (
                      <Button size="sm" variant="ghost" data-testid="keys-reset" disabled={readOnly || act.busy === 'save'} aria-label={`Reset the keys of: ${l.action}`} mix={on('click', () => void save(without(l.id)))}>
                        Reset
                      </Button>
                    ) : null}
                  </>
                )}
              </li>
            );
          })}
        </ul>
        {Object.keys(b).length > 0 ? (
          <div>
            <Button size="sm" data-testid="keys-reset-all" disabled={readOnly || act.busy === 'save'} mix={on('click', () => void save({}))}>
              Reset all
            </Button>
          </div>
        ) : null}
        <ErrorNote error={act.error} what="The keys were not saved" />
      </Group>
    );
  };
}
