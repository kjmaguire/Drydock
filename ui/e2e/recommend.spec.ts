// Skills picked in the workspace Library go to the project, where the team lead recommends which to use and for whom
// (decision P52, on the owner's request 2026-10-14). Against the real core; the lead is the harness's scripted stand-in agent
// (a test double), whose answer recommends the stand-in Wrangler skill for the whole team and one made-up skill the core drops.
import { expect, test } from '@playwright/test';
import { createAgent, createProject, freshRepo, workspaceNav } from './helpers';

test('picked Library skills reach the project; the lead recommends which to use; applying one adds it for the whole team', async ({ page }) => {
  await createProject(page, 'reco', freshRepo());
  await createAgent(page, 'Rex', 'lead');

  // Library > Skills: the registry tile is one short line; Sync now fills it from the harness's registry fixture
  await workspaceNav(page, 'nav-library-skills');
  const cf = page.locator('[data-testid=lib-skill-registry-tile][data-registry=cloudflare-skills]');
  await cf.click();
  const panel = page.locator('[data-testid=lib-skill-registry][data-registry=cloudflare-skills]');
  await panel.getByRole('button', { name: 'Sync now' }).click();
  await expect(cf).toContainText('3 skills');
  const row = (name: string) => panel.locator(`[data-testid=lib-skill-tile][data-name="${name}"]`);
  await expect(row('wrangler')).toBeVisible();
  // a row is one line until it is opened
  await expect(row('wrangler').getByTestId('lib-skill-tile-detail')).toHaveCount(0);
  await row('wrangler').locator('button.what').click();
  await expect(row('wrangler').getByTestId('lib-skill-tile-detail')).toContainText('Stand-in Wrangler rules for the browser tests.');

  // pick two and take them to the project
  await row('wrangler').getByRole('checkbox').check();
  await row('durable-objects').getByRole('checkbox').check();
  await panel.getByTestId('lib-skill-add-picked').click();
  await expect(page).toHaveURL(/\/skills$/);
  const picks = page.getByTestId('skill-picks');
  await expect(picks).toContainText('You picked 2 skills in the Library for reco');
  await picks.getByRole('button', { name: 'Ask Rex which to use' }).click();

  // the lead's answer: one recommendation kept, the made-up one left out with the reason
  await expect(page.getByTestId('recommend-summary')).toHaveText('The plan deploys Workers: add Wrangler for the whole team; Durable Objects can wait.', { timeout: 60_000 });
  const item = page.getByTestId('recommend-item');
  await expect(item).toHaveCount(1);
  await expect(item).toContainText('Add the skill wrangler for the whole team');
  await expect(item).toContainText('Every task deploys a Worker.');
  await expect(page.getByTestId('recommend-latest')).toContainText('1 suggestion was left out');
  await item.getByTestId('recommend-apply').click();
  await expect(item).toHaveAttribute('data-status', 'applied');
  const wrangler = page.locator('[data-testid=skill-tile][data-name=wrangler]');
  await expect(wrangler).toHaveAttribute('data-status', 'accepted');
  await expect(wrangler).toContainText('Accepted ✓ · whole team');
  await expect(page.locator('[data-testid=skill-tile][data-name=durable-objects]')).toHaveCount(0);
});
