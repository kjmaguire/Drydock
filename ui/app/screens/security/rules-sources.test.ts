import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { CLONE_COMMAND, DEMO_RULE, RULES_READ_ON, RULE_SOURCES, repoUrl } from './rules-sources.ts';

describe('where the Code engine\u2019s rules can come from (decision P44)', () => {
  it('lists each public GitHub repository once, with a licence and what its rules are for', () => {
    const repos = RULE_SOURCES.map((s) => s.repo);
    expect(new Set(repos).size).toBe(repos.length);
    expect(repos.length).toBeGreaterThan(0);
    for (const s of RULE_SOURCES) {
      expect(s.repo).toMatch(/^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/);
      expect(repoUrl(s.repo)).toBe(`https://github.com/${s.repo}`);
      expect(s.licence.trim()).not.toBe('');
      expect(s.covers.trim()).not.toBe('');
      if (s.terms !== undefined) expect(s.terms).toMatch(/^https:\/\/[a-z0-9.-]+\//);
    }
  });

  it('keeps what decision P44 and docs/security-scan.md say about the Opengrep and Semgrep sets, and does not call any set free to use', () => {
    const by = (repo: string) => RULE_SOURCES.find((s) => s.repo === repo)!;
    // the reason Drydock fetches no rules: the Opengrep set carries the Commons Clause and says it is for research, testing and benchmarking
    expect(by('opengrep/opengrep-rules').licence).toContain('Commons Clause');
    expect(by('opengrep/opengrep-rules').licence).toContain('research, testing and benchmarking');
    // the Semgrep set's licence file only points to its terms: the page says so and links them instead of paraphrasing them
    expect(by('semgrep/semgrep-rules').licence).toContain('Semgrep Rules License v1.0');
    expect(by('semgrep/semgrep-rules').terms).toBe('https://semgrep.dev/legal/rules-license');
    for (const s of RULE_SOURCES) expect(`${s.licence} ${s.covers}`).not.toMatch(/\b(free|recommended|safe to use)\b/i);
    expect(RULES_READ_ON).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });

  it('shows a Git command that clones one repository into a folder, and a rule with the five fields a Semgrep rule needs', () => {
    expect(CLONE_COMMAND).toBe('git clone -c core.longpaths=true --depth 1 https://github.com/OWNER/REPOSITORY C:\\tools\\REPOSITORY');
    for (const key of ['rules:', '- id:', '  pattern:', '  message:', '  languages:', '  severity:']) expect(DEMO_RULE).toContain(key);
    expect(DEMO_RULE.endsWith('\n')).toBe(true);
  });
});
