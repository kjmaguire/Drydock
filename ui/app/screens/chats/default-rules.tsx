// Allow rules for every new chat (D9): the person sets them once, visibly. chat.create copies them into each new Claude Code chat,
// where they are listed under Allowed tools and removable like the chat's own; chats that exist keep their own rules. Shown on
// Settings > Agent CLIs (Claude Code's Overview), the one place they are listed together.

import { css, on, type Handle } from 'remix/component';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { SettingRow, SettingsGroup } from '../../ui/master-detail.tsx';
import { t } from '../../ui/tokens.ts';

const listCss = css({ display: 'grid', gap: '4px', margin: '0', padding: '0', listStyle: 'none', '& li': { display: 'flex', alignItems: 'center', gap: t.s2 }, '& code': { fontSize: t.textSm } });
const quiet = css({ color: t.textMuted, fontSize: t.textSm });

/** The rules every new chat starts with, read again when they change; [] from a core that has none. */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function useDefaultRules<P extends Record<string, any>>(handle: Handle<P>) {
  const s = services(handle);
  return query(handle, {
    load: async (): Promise<string[]> => {
      try {
        return await s.client.chatDefaultRules();
      } catch {
        return [];
      }
    },
    when: (e) => e.type === 'chat.default_rules',
  });
}

export function ChatDefaultRules(handle: Handle<{ readOnly?: boolean }>) {
  const s = services(handle);
  const rules = useDefaultRules(handle);
  const act = action(handle);
  let typed = '';
  const set = async (rule: string, allow: boolean) => {
    const next = await act.run(`${allow ? 'add' : 'remove'}:${rule}`, () => s.client.chatSetDefaultRule(rule, allow));
    if (next) {
      rules.set(next);
      if (allow) typed = '';
    }
    void handle.update();
  };
  return () => {
    const list = rules.data ?? [];
    return (
      <SettingsGroup title="Chats" testId="chat-default-rules" label="Chats">
        <SettingRow label="Allowed in every new chat">
          <div style={{ display: 'grid', gap: '8px' }}>
            <span mix={quiet}>Each new Claude Code chat starts with these allow rules. It lists them under Allowed tools, where they can be removed. Chats you have now keep their own.</span>
            {list.length ? (
              <ul mix={listCss} aria-label="Allowed in every new chat">
                {list.map((r) => (
                  <li key={r}>
                    <code>{r}</code>
                    <Button size="sm" variant="ghost" disabled={!!act.busy || handle.props.readOnly} aria-label={`Remove ${r} from every new chat`} mix={on('click', () => void set(r, false))}>
                      Remove
                    </Button>
                  </li>
                ))}
              </ul>
            ) : (
              <span mix={quiet}>None. A chat's own rules are set from its refusals or under Allowed tools.</span>
            )}
            <form
              style={{ display: 'flex', gap: '6px' }}
              mix={on('submit', (e) => {
                e.preventDefault();
                const r = typed.trim();
                if (r) void set(r, true);
              })}
            >
              <input aria-label="Rule to allow in every new chat" placeholder="Bash(npm test)" value={typed} spellcheck={false} style={{ flex: '1', fontFamily: 'var(--font-mono)' }} mix={on('input', (e) => ((typed = e.currentTarget.value), void handle.update()))} />
              <Button type="submit" size="sm" disabled={!!act.busy || handle.props.readOnly}>
                Allow
              </Button>
            </form>
            <ErrorNote error={act.error} what="Could not change the rules" />
          </div>
        </SettingRow>
      </SettingsGroup>
    );
  };
}
