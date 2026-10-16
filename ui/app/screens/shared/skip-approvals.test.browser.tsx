import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import type { Handle } from 'remix/component';
import { SKIP_CONFIRM_LABEL } from '../../lib/permission.ts';
import { byLabel, cleanup, mount } from '../../test/render.tsx';
import { SkipApprovals, SkipsBadge } from './skip-approvals.tsx';

afterEach(cleanup);

const refusal = new Error('needs your confirmation: the permission mode "bypassPermissions" lets this agent act without asking you first. Confirm it (confirmSkipsApprovals) to use it');

/** A form around the component that keeps the state the way the real forms do (the mode the box is ticked for). */
function Form(handle: Handle<{ mode: string; error?: Error; changes: string[] }>) {
  let confirmed = '';
  return () => (
    <div>
      <SkipApprovals
        mode={handle.props.mode}
        confirmed={confirmed}
        onConfirm={(m) => {
          confirmed = m;
          handle.props.changes.push(m);
          void handle.update();
        }}
        error={handle.props.error}
      />
    </div>
  );
}

describe('SkipApprovals', () => {
  it('shows nothing for a permission mode that keeps approvals', () => {
    for (const mode of ['', 'default', 'acceptEdits', 'plan']) {
      const r = mount(<Form mode={mode} changes={[]} />);
      expect(r.$('[data-testid="skip-approvals"]')).toBeNull();
      cleanup();
    }
  });

  it('warns in plain words and asks for the box, for each of the three modes', () => {
    for (const mode of ['bypassPermissions', 'auto', 'dontAsk']) {
      const r = mount(<Form mode={mode} changes={[]} />);
      const box = r.$('[data-testid="skip-approvals"]')!;
      expect(box.getAttribute('role')).toBe('group');
      expect(box.getAttribute('data-mode')).toBe(mode);
      const warning = box.querySelector('[data-testid="skip-approvals-warning"]')!.textContent!;
      expect(warning).toContain('This mode skips approvals.');
      expect(warning).toContain(`The permission mode "${mode}" lets this agent act without asking you first.`);
      expect(warning).toContain('Nothing keeps it inside its working folder except its own behavior.');
      expect(warning).toContain('your approval of a merge still apply');
      const check = byLabel<HTMLInputElement>(box, SKIP_CONFIRM_LABEL);
      expect(check.type).toBe('checkbox');
      expect(check.checked).toBe(false);
      cleanup();
    }
    expect(SKIP_CONFIRM_LABEL).toBe('I understand this agent will act without asking me first');
  });

  it('reports the mode when the box is ticked and an empty mode when it is cleared, and the box belongs to that mode', async () => {
    const changes: string[] = [];
    const r = mount(<Form mode="auto" changes={changes} />);
    const check = byLabel<HTMLInputElement>(r.container, SKIP_CONFIRM_LABEL);
    await r.act(() => check.click());
    expect(changes).toEqual(['auto']);
    expect(byLabel<HTMLInputElement>(r.container, SKIP_CONFIRM_LABEL).checked).toBe(true);
    await r.act(() => byLabel<HTMLInputElement>(r.container, SKIP_CONFIRM_LABEL).click());
    expect(changes).toEqual(['auto', '']);
    expect(byLabel<HTMLInputElement>(r.container, SKIP_CONFIRM_LABEL).checked).toBe(false);
  });

  it('is unticked for a mode other than the one it was ticked for', async () => {
    const r = mount(
      <SkipApprovals mode="dontAsk" confirmed="bypassPermissions" onConfirm={() => undefined} />,
    );
    expect(byLabel<HTMLInputElement>(r.container, SKIP_CONFIRM_LABEL).checked).toBe(false);
    cleanup();
    const same = mount(<SkipApprovals mode="dontAsk" confirmed="dontAsk" onConfirm={() => undefined} />);
    expect(byLabel<HTMLInputElement>(same.container, SKIP_CONFIRM_LABEL).checked).toBe(true);
  });

  it('names who acts without asking', () => {
    const r = mount(<SkipApprovals mode="auto" confirmed="" onConfirm={() => undefined} who="agents made from Payments dev" />);
    expect(r.$('[data-testid="skip-approvals-warning"]')!.textContent).toContain('lets agents made from Payments dev act without asking you first');
  });

  it('shows the core\'s refusal next to the box, tied to it, as the field error', () => {
    const r = mount(<Form mode="bypassPermissions" error={refusal} changes={[]} />);
    const err = r.$('[data-testid="skip-approvals-error"]')!;
    expect(err.getAttribute('role')).toBe('alert');
    expect(err.textContent).toBe(refusal.message);
    const check = byLabel<HTMLInputElement>(r.container, SKIP_CONFIRM_LABEL);
    expect(check.getAttribute('aria-invalid')).toBe('true');
    expect(check.getAttribute('aria-describedby')).toBe(err.id);
    // the error sits in the same group as the box
    expect(err.closest('[data-testid="skip-approvals"]')!.contains(check)).toBe(true);
  });

  it('still shows a refusal for a mode the form did not know skips approvals, without a box it cannot answer', () => {
    const r = mount(<Form mode="yolo" error={refusal} changes={[]} />);
    expect(r.$('[data-testid="skip-approvals-error"]')!.textContent).toBe(refusal.message);
    expect(r.container.querySelector('input[type="checkbox"]')).toBeNull();
  });

  it('can be switched off', () => {
    const r = mount(<SkipApprovals mode="auto" confirmed="" onConfirm={() => undefined} disabled />);
    expect(byLabel<HTMLInputElement>(r.container, SKIP_CONFIRM_LABEL).disabled).toBe(true);
  });
});

describe('SkipsBadge', () => {
  it('is the red "skips approvals" for the three modes and nothing for any other', () => {
    for (const mode of ['bypassPermissions', 'auto', 'dontAsk']) {
      const r = mount(<SkipsBadge mode={mode} />);
      const badge = r.$('[data-testid="skips-approvals-badge"]')!;
      expect(badge.textContent).toBe('skips approvals');
      expect(badge.getAttribute('data-tone')).toBe('err');
      expect(badge.getAttribute('title')).toContain(mode);
      cleanup();
    }
    for (const mode of ['', 'default', 'acceptEdits', 'plan', undefined]) {
      const r = mount(<SkipsBadge mode={mode} />);
      expect(r.$('[data-testid="skips-approvals-badge"]')).toBeNull();
      cleanup();
    }
  });
});
