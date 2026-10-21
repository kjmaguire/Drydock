// "Where to get more rules" under the Security page's own rules folder. The built-in rules run on every scan (decision P77); this
// lists public repositories of Semgrep-syntax rules a person can add on top, with what each one's own licence says, how to get a
// set with Git, and one small rule to try. Everything is text: nothing is fetched, and the links open in the person's browser.

import { css, type Handle } from 'remix/component';
import { LearnMore } from '../../ui/learn-more.tsx';
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

export function RulesSources(_handle: Handle) {
  return () => (
    <details mix={helpCss} data-testid="rules-sources">
      <summary>Where to get more rules</summary>
      <div class="body">
        <p>
          The built-in rules run on every scan. To add more, get a set yourself and choose its folder above; Semgrep rule sets work unchanged. What each licence said on{' '}
          {RULES_READ_ON} is below; a licence can change, so read it before you use a set. <LearnMore topic="rule-licences" />
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
}
