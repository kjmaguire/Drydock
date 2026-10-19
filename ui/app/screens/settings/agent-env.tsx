// The environment agent programs start with: an allowlist, plus variable NAMES the human chose to pass on. Never values.
//
// Every line is checked here before anything is sent (audit 2026-10-16, L38): a person who pastes `AWS_SECRET_ACCESS_KEY=abc...`
// has put a secret where a name belongs. The page says which line is not a name, sends nothing, and never repeats the line.

import { css, on, ref, type Handle } from 'remix/component';
import { Variable } from 'lucide';
import type { AgentEnv } from '../../api/libraryClient.ts';
import { action, query, services } from '../../state/services.tsx';
import { Button } from '../../ui/button.tsx';
import { faint, mono, muted } from '../../ui/data.tsx';
import { ErrorNote } from '../../ui/feedback.tsx';
import { errorAttrs, Field, formActions } from '../../ui/form.tsx';
import { LearnMore } from '../../ui/learn-more.tsx';
import { t } from '../../ui/tokens.ts';
import { parseEnvNames } from './settings.ts';
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
  /** Which line of the box is not a name: the line's number, never its text. */
  let error: string | undefined;
  let form: HTMLElement | undefined;

  const save = async () => {
    saved = false;
    const parsed = parseEnvNames(text);
    if (!parsed.ok) {
      // refused here: the core is not asked, and the keyboard goes to the box
      error = parsed.error;
      act.clear();
      void handle.update();
      handle.queueTask(() => form?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
      return;
    }
    error = undefined;
    const r = await act.run('save', () => s.client.agentEnvSet(parsed.names));
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
      error = undefined;
    }
    return (
      <Group
        title="What agents inherit"
        icon={Variable}
        label="Agents' environment"
        testId="agent-env"
        description={
          <>
            Agent programs start with a trimmed environment: a token another tool left in yours is not passed on. <LearnMore topic="agent-environment" />
          </>
        }
      >
        <ErrorNote error={q.error ?? act.error} what="Environment" />
        <div mix={panelPad}>
          <form mix={[formCss, ref((n: Element) => (form = n instanceof HTMLElement ? n : undefined)), on('submit', (e) => (e.preventDefault(), void save()))]}>
            <Field label="Also pass these variables to agents (names only, one per line; a value is refused)" error={error}>
              {(id) => (
                <textarea
                  id={id}
                  rows={3}
                  spellcheck={false}
                  placeholder={'AWS_PROFILE\nCLAUDE_CODE_USE_BEDROCK'}
                  value={text}
                  {...errorAttrs(id, error)}
                  mix={[mono, on('input', (e) => ((text = e.currentTarget.value), (saved = false), (error = undefined), void handle.update()))]}
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
