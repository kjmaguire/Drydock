import { expect, test } from '@playwright/test';
import { choose, createProject, projectNav, workspaceNav } from './helpers';

// A permission mode that lets an agent act without asking (audit 2026-10-16, M1; docs/security.md s4), against the real Go core.
// The core gives such a mode to a template or an agent only when the request carries the person's confirmation
// (confirmSkipsApprovals), so the Library's template form and the Team page keep their button off until the box is ticked, send
// the confirmation only then, and the template and the agent made from it keep the red badge.
const BOX = 'I understand this agent will act without asking me first';

test('a permission mode that skips approvals needs the box ticked; the template and the agent made from it carry the badge', async ({ page }) => {
  await createProject(page, 'skip-mode');

  // the Library's template form: a mode that keeps approvals asks nothing
  await workspaceNav(page, 'nav-library');
  await page.getByRole('button', { name: 'New agent template' }).click();
  const tf = page.getByTestId('template-form');
  await tf.getByLabel('Template name').fill('Free hand');
  await tf.getByLabel('Role').fill('backend');
  const create = tf.getByRole('button', { name: 'Create template' });
  await choose(tf.getByLabel('Permission mode', { exact: true }), 'acceptEdits');
  await expect(tf.getByTestId('skip-approvals')).toHaveCount(0);
  await expect(create).toBeEnabled();

  // one that skips approvals shows its warning and keeps the button off until the person ticks the box
  await choose(tf.getByLabel('Permission mode', { exact: true }), 'bypassPermissions');
  const warning = tf.getByTestId('skip-approvals');
  await expect(warning).toContainText('This mode skips approvals.');
  await expect(warning).toContainText('act without asking you first');
  await expect(create).toBeDisabled();
  await warning.getByLabel(BOX).check();
  await expect(create).toBeEnabled();
  // the box belongs to its mode: another mode that skips approvals asks again
  await choose(tf.getByLabel('Permission mode', { exact: true }), 'dontAsk');
  await expect(warning.getByLabel(BOX)).not.toBeChecked();
  await expect(create).toBeDisabled();
  await choose(tf.getByLabel('Permission mode', { exact: true }), 'bypassPermissions');
  await warning.getByLabel(BOX).check();
  await create.click();

  // the core took it (it refuses a mode without the confirmation), and the template keeps the red badge on its tile and its panel
  const tile = page.getByTestId('lib-template-tile').filter({ hasText: 'Free hand' });
  await expect(tile.getByTestId('tile-badge')).toHaveText('skips approvals');
  await expect(page.getByTestId('lib-template').filter({ hasText: 'Free hand' }).getByTestId('skips-approvals-badge')).toHaveText('skips approvals');
  await expect(tf).toHaveCount(0);

  // the template joins the team from the Team page: again only after the box is ticked
  await projectNav(page, 'nav-team');
  await expect(page.getByLabel('Template', { exact: true })).toContainText('Free hand');
  const add = page.getByRole('button', { name: 'Add to this team' });
  await expect(page.getByTestId('skip-approvals')).toContainText('bypassPermissions');
  await expect(add).toBeDisabled();
  await page.getByTestId('skip-approvals').getByLabel(BOX).check();
  await expect(add).toBeEnabled();
  await add.click();
  const card = page.getByTestId('agent').filter({ hasText: 'Free hand' });
  await expect(card).toBeVisible();
  await expect(card.getByTestId('skips-approvals-badge')).toHaveText('skips approvals');

  // the badge stays: on the agent's own page after a reload
  await page.reload();
  await page.getByTestId('agent').filter({ hasText: 'Free hand' }).getByTestId('agent-link').click();
  await expect(page.getByRole('heading', { level: 1, name: 'Free hand' })).toBeVisible();
  await expect(page.getByTestId('skips-approvals-badge').first()).toHaveText('skips approvals');
});
