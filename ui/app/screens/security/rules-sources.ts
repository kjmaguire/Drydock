// Where the Code engine's rules can come from. Drydock ships and downloads no rules (decision P44): the person chooses a folder of
// rules they are licensed to use. This is the Security page's help for that choice: public repositories of Semgrep-syntax rules
// (Opengrep reads them unchanged) and what each one's own licence file or README says. The wording was read from the
// repositories on RULES_READ_ON (docs/security-scan.md, section 5) and a licence can change, so the page says to read it. Nothing
// here is fetched, and nothing here is a recommendation or legal advice.

/** The day the licences below were read from the repositories. */
export const RULES_READ_ON = '2026-10-16';

/** A public repository of rules written for Semgrep. */
export interface RuleSource {
  /** owner/repository on GitHub */
  repo: string;
  /** What the repository's licence file or README says, in few words. */
  licence: string;
  /** What the rules are for, as the repository describes them. */
  covers: string;
  /** The page with the licence's terms, when the repository's licence file only points to one. */
  terms?: string;
}

export const RULE_SOURCES: readonly RuleSource[] = [
  {
    repo: 'semgrep/semgrep-rules',
    licence: 'Semgrep Rules License v1.0; the repository\u2019s licence file points to its terms on semgrep.dev, so read them first.',
    covers: 'The rules Semgrep itself publishes.',
    terms: 'https://semgrep.dev/legal/rules-license',
  },
  {
    repo: 'opengrep/opengrep-rules',
    licence: 'LGPL-2.1 with the Commons Clause, which does not allow selling the rules or a paid product or service built on them. Its README says the rules are intended for research, testing and benchmarking.',
    covers: 'A fork of Semgrep\u2019s rules, kept by the Opengrep project.',
  },
  {
    repo: 'trailofbits/semgrep-rules',
    licence: 'AGPL-3.0.',
    covers: 'Trail of Bits\u2019 public rules, used in its security audits and vulnerability research.',
  },
  {
    repo: '0xdea/semgrep-rules',
    licence: 'MIT.',
    covers: 'A C (and sometimes C++) ruleset for vulnerability research.',
  },
  {
    repo: 'dgryski/semgrep-go',
    licence: 'MIT.',
    covers: 'Patterns for finding odd Go code.',
  },
];

/** The address of a source's repository. */
export const repoUrl = (repo: string): string => `https://github.com/${repo}`;

/** Getting a set with Git. core.longpaths lets Windows keep the deeper file names some sets have. */
export const CLONE_COMMAND = 'git clone -c core.longpaths=true --depth 1 https://github.com/OWNER/REPOSITORY C:\\tools\\REPOSITORY';

/**
 * One rule of Drydock's own, to try the Code engine before choosing a real set: save it as a .yaml file in an empty folder and
 * choose that folder. It flags each fmt.Println call in the Go files Opengrep scans. It is the shape of the rule in Opengrep's
 * own README (id, pattern, message, languages, severity); a test keeps it a valid Semgrep rule file.
 */
export const DEMO_RULE = `rules:
- id: demo-fmt-println
  pattern: fmt.Println(...)
  message: "Debug print left in the code"
  languages: [go]
  severity: INFO
`;
