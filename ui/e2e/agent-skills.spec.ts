// The owner's report of 2026-10-20: "we don't see the ability to add or remove skills from Agents when in the project", and
// "skills are agent-based, not entire project-based" (decision P76). Against the real core: on an agent's own page a skill is
// added straight from the Library for that agent alone, another agent does not get it, and Remove takes it away again.
import { expect, test } from '@playwright/test';
import { createAgent, createProjectWithoutRepo, projectNav, workspaceNav } from './helpers';

test('an agent page adds a Library skill for that agent only and removes it again', async ({ page }) => {
  await createProjectWithoutRepo(page, 'specialists');
  await createAgent(page, 'Ada', 'backend');
  await createAgent(page, 'Bea', 'frontend');

  // fill the Library from the harness's registry fixture
  await workspaceNav(page, 'nav-library-skills');
  const cf = page.locator('[data-testid=lib-skill-registry-tile][data-registry=cloudflare-skills]');
  await cf.click();
  await page.locator('[data-testid=lib-skill-registry][data-registry=cloudflare-skills]').getByRole('button', { name: 'Sync now' }).click();
  await expect(cf).toContainText('3 skills');

  // Ada's page: no skill yet; Add skills opens the picker with the Library's skills
  await projectNav(page, 'nav-team');
  await page.getByRole('link', { name: 'Ada', exact: true }).first().click();
  await expect(page.getByRole('heading', { level: 1, name: 'Ada' })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Skills' }).first()).toContainText('Ada has no skill yet.');
  await page.getByTestId('agent-skills-more').click();
  const choice = page.locator('[data-testid=agent-skill-choice][data-skill=wrangler]');
  await expect(choice).toHaveAttribute('data-from', 'library');
  await choice.getByTestId('agent-skill-add').click();
  await expect(page.getByTestId('agent-skill-added')).toHaveText('wrangler was given to Ada.');
  await expect(page.locator('[data-testid=agent-skill][data-skill=wrangler]')).toBeVisible();
  // once in the project it is no longer offered from the Library
  await expect(page.locator('[data-testid=agent-skill-choice][data-skill=wrangler]')).toHaveCount(0);

  // the project's Skills page: accepted, for one agent, and that agent is Ada
  await projectNav(page, 'nav-skills');
  const row = page.locator('[data-testid=skill-tile][data-name=wrangler]');
  await expect(row).toContainText('Accepted ✓ · 1 agent');
  await row.locator('button.what').click();
  await expect(row.getByTestId('skill-audience-text')).toHaveText('Ada');

  // Bea's page: she does not have it, and it is offered to her from the project
  await projectNav(page, 'nav-team');
  await page.getByRole('link', { name: 'Bea', exact: true }).first().click();
  await expect(page.getByRole('heading', { level: 1, name: 'Bea' })).toBeVisible();
  await expect(page.locator('[data-testid=agent-skill][data-skill=wrangler]')).toHaveCount(0);
  await page.getByTestId('agent-skills-more').click();
  await expect(page.locator('[data-testid=agent-skill-choice][data-skill=wrangler]')).toHaveAttribute('data-from', 'project');

  // back on Ada's page, Remove takes it away
  await projectNav(page, 'nav-team');
  await page.getByRole('link', { name: 'Ada', exact: true }).first().click();
  await page.locator('[data-testid=agent-skill][data-skill=wrangler]').getByTestId('agent-skill-remove').click();
  await expect(page.locator('[data-testid=agent-skill][data-skill=wrangler]')).toHaveCount(0);
  await projectNav(page, 'nav-skills');
  await expect(page.locator('[data-testid=skill-tile][data-name=wrangler]')).toContainText('Accepted ✓ · no agent yet');
});
