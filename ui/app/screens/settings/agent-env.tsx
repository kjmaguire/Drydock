// The environment agent programs start with: an allowlist, plus variable NAMES the human chose to pass on. Never values.

import { css, on, type Handle } from 'remix/component';
import { Variable } from 'lucide';
import type { AgentEnv } from '../../api/libraryClient.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { faint, mono, muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { Field, formActions } from '../../ui/form.tsx';
import { t } from '../../ui/tokens.ts';
import { envNames } from './settings.ts';
import { Group, panelPad } from './settings-kit.tsx';

const formCss = css({ display: 'grid', gap: t.s3 });
const detailsCss = css({ fontSize: t.textSm, '& p': { margin: `${t.s2} 0 0` } });

export function AgentEnvSection(handle: Handle) {
  const s = services(handle);
  const q = query(handle, { load: () => s.client.agentEnvGet(), when: (e) => e.type === 'agentenv.changed' });
  const act = action(handle);
  let text = '';
  let saved = false;
  let seen: AgentEnv | undefined;

  const save = async () => {
    saved = false;
    const r = await act.run('save', () => s.client.agentEnvSet(envNames(text)));
    if (r) {
      saved = true;
      q.reload();
    }
    void handle.update();
  };

  return () => {
    // a new answer from the core replaces the box's text (on load, after a save, when another window changed it)
    if (q.data && q.data !== seen) {
      seen = q.data;
      text = q.data.pass.join('\n');
    }
    return (
      <Group
        title="What agents inherit"
        icon={Variable}
        label="Agents' environment"
        testId="agent-env"
        description={
          <>
            Agent programs start with a trimmed environment, not everything Drydock was started with. A token another tool left in your environment (GitHub, a cloud
            provider) is not handed to an agent that a prompt could ask to print it. Claude Code keeps everything named <code>CLAUDE_*</code> or <code>ANTHROPIC_*</code>, so
            its own sign-in keeps working.
          </>
        }
      >
        <ErrorNote error={q.error ?? act.error} what="Environment" />
        <div mix={panelPad}>
          <form mix={[formCss, on('submit', (e) => (e.preventDefault(), void save()))]}>
            <Field label="Also pass these variables to agents (names only, one per line; a value is refused)">
              {(id) => (
                <textarea
                  id={id}
                  rows={3}
                  spellcheck={false}
                  placeholder={'AWS_PROFILE\nCLAUDE_CODE_USE_BEDROCK'}
                  value={text}
                  mix={[mono, on('input', (e) => ((text = e.currentTarget.value), (saved = false), void handle.update()))]}
                />
              )}
            </Field>
            <div mix={formActions}>
              <Button type="submit" variant="primary" disabled={act.busy !== ''}>
                Save
              </Button>
              {saved ? (
                <span role="status" mix={muted}>
                  Saved. Applies from the next agent start.
                </span>
              ) : null}
            </div>
          </form>
        </div>
        <details mix={detailsCss}>
          <summary>What every agent keeps</summary>
          <p mix={faint}>
            {(q.data?.kept ?? []).join(', ')}; and every variable starting with {(q.data?.prefixes ?? []).join(' or ')} for Claude Code.
          </p>
        </details>
      </Group>
    );
  };
}
