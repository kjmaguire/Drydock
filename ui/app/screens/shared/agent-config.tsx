// What an agent CLI would act on in the folders it works in, and the button that trusts it (audit 2026-10-16, M3;
// docs/security.md s3 item 2b). Two places show it: the project's settings (screens/project/project-agent-config.tsx, every
// finding, trusted ones as such) and the banner above the project's pages (shell/agent-config-banner.tsx, the ones that keep a
// CLI from starting). Both trust by the adapter and the fingerprint that were on screen, never by anything else.
//
// The file names are the repository's own text: they are shown as text (never as markup), with the characters that are not
// drawn written out (lib/visible.ts).

import { css, on, type Handle } from 'remix/component';
import { ShieldCheck } from 'lucide';
import type { AgentConfigFinding } from '../../api/types.ts';
import { hasHidden, visible } from '../../lib/visible.ts';
import { Button } from '../../ui/button.tsx';
import { Badge } from '../../ui/data.tsx';
import { Notice } from '../../ui/feedback.tsx';
import { t } from '../../ui/tokens.ts';
import { findingKey, trustKey } from './agent-config.ts';

const listCss = css({ margin: '0', padding: '0', listStyle: 'none', display: 'grid', minWidth: '0' });
const itemCss = css({
  display: 'grid',
  gap: t.s2,
  padding: `${t.s3} ${t.s4}`,
  borderTop: `1px solid ${t.border}`,
  minWidth: '0',
  '&:first-child': { borderTop: '0' },
  '& .head': { display: 'flex', alignItems: 'center', gap: t.s2, flexWrap: 'wrap', minWidth: '0' },
  '& .where': { color: t.textMuted, fontSize: t.textSm, overflowWrap: 'anywhere', minWidth: '0' },
  '& .end': { marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: t.s2 },
  '& ul': { margin: '0', paddingLeft: t.s5, display: 'grid', gap: '2px', fontSize: t.textSm },
  '& li code': { overflowWrap: 'anywhere' },
});

/**
 * The findings as a list: for each, the CLI, where it was found, the files, and either "trusted" or a Trust button. `busy` is
 * the trustKey of the trust that is running; `readOnly` turns trusting off (a read-only install shows, but does not trust).
 */
export function AgentConfigFindings(handle: Handle<{ findings: readonly AgentConfigFinding[]; busy: string; readOnly: boolean; onTrust: (f: AgentConfigFinding) => void; label: string; testId?: string }>) {
  return () => {
    const { findings, busy, readOnly, onTrust, label, testId = 'agent-config-findings' } = handle.props;
    const hidden = findings.some((f) => hasHidden(f.where) || f.files.some((x) => hasHidden(x)));
    return (
      <>
        {hidden ? (
          <Notice tone="warn" title="A name below has characters that are not drawn.">
            Characters that reorder or hide text are written out as <code>{'\\u{...}'}</code>, so the name is shown as it is.
          </Notice>
        ) : null}
        <ul mix={listCss} aria-label={label} data-testid={testId}>
          {findings.map((f) => (
            <li key={findingKey(f)} mix={itemCss} data-testid="agent-config-finding" data-adapter={f.adapter} data-trusted={f.trusted ? 'true' : 'false'}>
              <div class="head">
                <strong>{f.adapter}</strong>
                <span class="where">{visible(f.where)}</span>
                <span class="end">
                  {f.trusted ? (
                    <Badge tone="ok" testId="agent-config-trusted">
                      trusted
                    </Badge>
                  ) : (
                    <>
                      <Badge tone="warn">not trusted</Badge>
                      <Button
                        size="sm"
                        variant="secondary"
                        icon={ShieldCheck}
                        aria-label={`Trust ${f.adapter} configuration: ${f.where}`}
                        disabled={!!busy || readOnly || !f.fingerprint}
                        data-testid="agent-config-trust"
                        mix={on('click', () => onTrust(f))}
                      >
                        {busy === trustKey(f) ? 'Trusting...' : 'Trust'}
                      </Button>
                    </>
                  )}
                </span>
              </div>
              <ul aria-label={`Files ${f.adapter} would act on: ${f.where}`}>
                {f.files.map((file) => (
                  <li key={file}>
                    <code>{visible(file)}</code>
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
      </>
    );
  };
}
