// "Where do I get rules?" under the Security page's rules folder (decision P44: Drydock ships and downloads no rules). It lists
// public repositories of Semgrep-syntax rules with what each one's own licence says, how to get a set with Git, and one small
// rule to try the Code engine with. It is open while the folder has no usable rules and folds away once it has, unless the
// person opened or closed it themselves. Everything is text: nothing is fetched, and the links open in the person's browser.

import { css, on, type Handle } from 'remix/component';
import { t } from '../../ui/tokens.ts';
import { CLONE_COMMAND, DEMO_RULE, RULES_READ_ON, RULE_SOURCES, repoUrl } from './rules-sources.ts';

const helpCss = css({
  '& summary': { cursor: 'pointer', fontSize: t.textSm, color: t.textMuted },
  '& .body': { display: 'grid', gap: t.s2, marginTop: t.s2, fontSize: t.textSm, color: t.textMuted },
  '& p': { margin: '0' },
  '& ul': { margin: '0', padding: '0', listStyle: 'none', display: 'grid', gap: t.s2 },
  '& li': { display: 'grid', gap: '2px', minWidth: '0', overflowWrap: 'anywhere' },
  '& li strong': { fontWeight: '600', color: t.text },
  '& a': { color: t.accentText },
  '& pre': {
    margin: '0',
    padding: `${t.s2} ${t.s3}`,
    border: `1px solid ${t.border}`,
    borderRadius: t.rMd,
    background: t.bgApp,
    color: t.text,
    fontFamily: t.mono,
    fontSize: t.textSm,
    overflowX: 'auto',
    // one click selects the whole command or rule, to copy it
    userSelect: 'all',
  },
});

export function RulesSources(handle: Handle<{ openByDefault: boolean }>) {
  // set once the person opens or closes it: from then on their choice stands
  let chosen: boolean | undefined;
  return () => {
    const open = chosen ?? handle.props.openByDefault;
    return (
      <details
        mix={[
          helpCss,
          on('toggle', (e) => {
            // the page changing `open` by itself (rules found) also fires toggle: that is not a choice
            const now = (e.currentTarget as HTMLDetailsElement).open;
            if (now === (chosen ?? handle.props.openByDefault)) return;
            chosen = now;
            void handle.update();
          }),
        ]}
        open={open}
        data-testid="rules-sources"
      >
        <summary>Where do I get rules?</summary>
        <div class="body">
          <p>
            Opengrep reads rules written for Semgrep, so a Semgrep rule set works unchanged. Drydock downloads none: you get a set yourself and choose the folder it is in. These public
            repositories hold sets, with what each one&rsquo;s own licence file or README said on {RULES_READ_ON}. A licence can change, so read it yourself and use a set only in the way
            it allows.
          </p>
          <ul aria-label="Rule sets">
            {RULE_SOURCES.map((s) => (
              <li key={s.repo} data-testid="rules-source" data-repo={s.repo}>
                <span>
                  <strong>
                    <a href={repoUrl(s.repo)} target="_blank" rel="noopener noreferrer" data-rmx-document>
                      {s.repo}
                    </a>
                  </strong>
                  : {s.covers}
                </span>
                <span>
                  Licence: {s.licence}
                  {s.terms ? (
                    <>
                      {' '}
                      <a href={s.terms} target="_blank" rel="noopener noreferrer" data-rmx-document>
                        Terms
                      </a>
                    </>
                  ) : null}
                </span>
              </li>
            ))}
          </ul>
          <p>To get a set with Git, replace OWNER/REPOSITORY with one of the above, then choose the folder:</p>
          <pre data-testid="rules-clone">{CLONE_COMMAND}</pre>
          <p>To try the Code engine first, save this as demo.yaml in an empty folder and choose that folder. A scan then flags each fmt.Println call in the Go files it scans:</p>
          <pre data-testid="rules-demo">{DEMO_RULE}</pre>
        </div>
      </details>
    );
  };
}
