// The first-run notice of decision D1: Drydock drives the person's own, signed-in agent CLIs, Claude Code first, under the
// terms of their own agreement with the provider, and never reads, copies or sends their credentials. It stays at the top of
// the window until the person says they understand (notice.acknowledge, kept with the version of the text and audited); it
// cannot be closed any other way. While the core's answer is not in (or it failed) nothing is drawn: a notice must not put
// an error bar over every page.
import { css, on, type Handle } from 'remix/component';
import { starts } from '../state/live.ts';
import { action, query, services } from '../state/services.tsx';
import { Button } from '../ui/button.tsx';
import { t, tone } from '../ui/tokens.ts';

export const CLAUDE_TERMS = 'claude-terms';

const noticeCss = css({
  display: 'flex',
  alignItems: 'center',
  flexWrap: 'wrap',
  gap: t.s3,
  padding: `${t.s2} ${t.s4}`,
  fontSize: t.textMd,
  borderBottom: `1px solid ${t.border}`,
  '& p': { margin: '0', flex: '1 1 480px', minWidth: '0' },
  '& a': { fontWeight: '600', color: 'inherit', textDecoration: 'underline' },
});

export function TermsNotice(handle: Handle) {
  const s = services(handle);
  const status = query(handle, { load: () => s.client.noticeStatus(), when: (e) => starts(e, 'notice.') });
  const act = action(handle);
  return () => {
    const n = status.data?.notices.find((x) => x.id === CLAUDE_TERMS);
    if (!n || n.acknowledged) return null;
    return (
      <section mix={noticeCss} style={{ background: tone.info.bg, color: t.text }} aria-label="Your agents run as you" data-testid="terms-notice">
        <p>
          <strong>Your agents run as you.</strong> Drydock starts your own Claude Code CLI, signed in with your own Anthropic account, and never reads, copies or
          sends your Claude credentials. What the agents do counts against your own plan, and your use of Claude Code through Drydock is under your agreement
          with Anthropic: the{' '}
          <a href="https://www.anthropic.com/legal/consumer-terms">Consumer Terms</a> or the <a href="https://www.anthropic.com/legal/commercial-terms">Commercial Terms</a>,
          and the <a href="https://www.anthropic.com/legal/aup">Usage Policy</a>. Codex, Gemini CLI, OpenCode and Goose likewise use your own sign-in, under their
          providers' terms.
        </p>
        <Button
          variant="primary"
          size="sm"
          disabled={act.busy !== ''}
          mix={on('click', async () => {
            if (await act.run('ack', () => s.client.noticeAcknowledge(n.id, n.version))) await status.reload();
          })}
        >
          I understand
        </Button>
        {act.error ? <span role="alert">{act.error instanceof Error ? act.error.message : String(act.error)}</span> : null}
      </section>
    );
  };
}
