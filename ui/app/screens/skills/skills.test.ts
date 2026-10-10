import { expect } from 'remix/assert';
import { describe, it } from 'remix/test';
import { audienceText, byRole, coverageText, inWords, isGiven, listOr, noSkillChanges, onTeam, reaches, rolesMatch, scanText, skillTone, toggleSkill } from './skills.ts';

describe('Skills helpers', () => {
  it('reports a scan with counts and the problems verbatim', () => {
    expect(
      scanText({
        found: 2,
        accepted: 0,
        offered: 1,
        blocked: 1,
        removed: 0,
        errors: null,
      }),
    ).toBe('Found 2: 1 offered, 0 accepted, 1 blocked, 0 removed.');
    expect(
      scanText({
        found: 0,
        accepted: 0,
        offered: 0,
        blocked: 0,
        removed: 0,
        errors: ['bad yaml', 'too big'],
      }),
    ).toContain('Problems: bad yaml; too big');
  });

  it('tones, coverage and lists', () => {
    expect(skillTone('accepted')).toBe('ok');
    expect(skillTone('offered')).toBe('warn');
    expect(skillTone('blocked')).toBe('err');
    expect(skillTone('rejected')).toBe('muted');
    expect(coverageText({ files: 2, tool: 1 })).toBe('2 delivered as rule files, 1 through a tool.');
    expect(coverageText(undefined)).toBe('');
    expect(listOr(null)).toBe('all');
    expect(listOr(['a', 'b'])).toBe('a, b');
  });

  it('matches roles as the core does: none listed is every role, case and spaces ignored', () => {
    expect(rolesMatch([], 'anything')).toBe(true);
    expect(rolesMatch(null, '')).toBe(true);
    expect(rolesMatch(['Frontend'], ' frontend ')).toBe(true);
    expect(rolesMatch(['UX Designer', 'QA'], 'qa')).toBe(true);
    expect(rolesMatch(['Frontend'], 'backend')).toBe(false);
  });

  it('decides who a skill reaches like internal/skills Reaches', () => {
    const team = { audience: 'team', roles: ['Frontend'], agents: ['agt_b'] };
    expect(reaches(team, 'agt_a', 'frontend')).toBe(true); // by role
    expect(reaches(team, 'agt_b', 'backend')).toBe(true); // given on top of the team
    expect(reaches(team, 'agt_c', 'backend')).toBe(false);
    expect(byRole(team, 'frontend')).toBe(true);
    expect(byRole(team, 'backend')).toBe(false);
    const chosen = { audience: 'agents', roles: [], agents: ['agt_b'] };
    expect(reaches(chosen, 'agt_b', 'backend')).toBe(true);
    expect(reaches(chosen, 'agt_a', 'backend')).toBe(false); // roles do not count for chosen agents
    expect(byRole(chosen, 'backend')).toBe(false);
    expect(reaches({ audience: 'agents', roles: null, agents: null }, '', 'qa')).toBe(false);
  });

  it('says who gets a skill in words', () => {
    const names: Record<string, string> = { agt_a: 'Ada', agt_b: 'Bob', agt_c: 'Cy' };
    const name = (id: string) => names[id] ?? id;
    expect(audienceText({ audience: 'team', roles: [], agents: [] }, name)).toBe('The whole team');
    expect(audienceText({ audience: 'team', roles: ['Database', 'Backend'], agents: ['agt_a'] }, name)).toBe('Every Database or Backend agent, and Ada');
    expect(audienceText({ audience: 'agents', roles: ['QA'], agents: ['agt_a', 'agt_b', 'agt_c'] }, name)).toBe('Only Ada, Bob and Cy');
    expect(audienceText({ audience: 'agents', roles: null, agents: null }, name)).toBe('Nobody: no agent is chosen yet');
    expect(inWords([])).toBe('');
    expect(inWords(['a', 'b'], 'or')).toBe('a or b');
  });

  it('leaves out agents removed from the team once the team is known', () => {
    const k = { audience: 'agents', roles: [], agents: ['agt_a', 'agt_gone'] };
    expect(onTeam(k, [{ id: 'agt_a' }]).agents).toEqual(['agt_a']);
    expect(onTeam(k, []).agents).toEqual(['agt_a', 'agt_gone']);
  });

  it('keeps only real changes when skills are ticked and unticked', () => {
    let c = noSkillChanges();
    c = toggleSkill(c, 'new', false, true); // give a skill the agent did not have
    c = toggleSkill(c, 'old', true, false); // take one it had
    expect(c).toEqual({ add: ['new'], remove: ['old'] });
    expect(isGiven(c, 'new', false)).toBe(true);
    expect(isGiven(c, 'old', true)).toBe(false);
    expect(isGiven(c, 'kept', true)).toBe(true);
    c = toggleSkill(toggleSkill(c, 'new', false, false), 'old', true, true); // and back: nothing to do
    expect(c).toEqual({ add: [], remove: [] });
    expect(toggleSkill(toggleSkill(noSkillChanges(), 'x', false, true), 'x', false, true)).toEqual({ add: ['x'], remove: [] });
  });
});
