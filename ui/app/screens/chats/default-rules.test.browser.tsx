import { expect } from 'remix/assert';
import { afterEach, describe, it } from 'remix/test';
import { byLabel, byText, cleanup, mount, testServices, type, waitFor } from '../../test/render.tsx';
import { ChatDefaultRules } from './default-rules.tsx';

afterEach(cleanup);

describe('Allowed in every new chat (D9, Settings > Agent CLIs)', () => {
  it('lists the rules, adds one checked by the core, and removes one', async () => {
    let rules = ['Bash(npm test)'];
    const calls: unknown[][] = [];
    // TEST DOUBLE: the core's checker refuses a bare Bash
    const s = testServices({
      chatDefaultRules: async () => rules,
      chatSetDefaultRule: async (tool, allow) => {
        calls.push([tool, allow]);
        if (tool === 'Bash') throw new Error('a bare Bash rule would allow every command');
        rules = allow ? [...rules, tool] : rules.filter((r) => r !== tool);
        return rules;
      },
    });
    const r = mount(<ChatDefaultRules />, s);
    await waitFor(() => expect(r.$$('[data-testid="chat-default-rules"] li code').length).toBe(1), r);
    await r.act(() => type(byLabel(r.container, 'Rule to allow in every new chat'), 'Bash'));
    await r.act(() => byText(r.container, 'button', 'Allow').click());
    await waitFor(() => expect(r.container.textContent).toContain('a bare Bash rule would allow every command'), r);
    await r.act(() => type(byLabel(r.container, 'Rule to allow in every new chat'), 'Edit'));
    await r.act(() => byText(r.container, 'button', 'Allow').click());
    await waitFor(() => expect(Array.from(r.$$('li code')).map((e) => e.textContent)).toEqual(['Bash(npm test)', 'Edit']), r);
    await r.act(() => (r.$('[aria-label="Remove Bash(npm test) from every new chat"]') as HTMLElement).click());
    await waitFor(() => expect(Array.from(r.$$('li code')).map((e) => e.textContent)).toEqual(['Edit']), r);
    expect(calls).toEqual([
      ['Bash', true],
      ['Edit', true],
      ['Bash(npm test)', false],
    ]);
  });
});
