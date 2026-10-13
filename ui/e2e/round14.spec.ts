import { expect, test, type Page } from '@playwright/test';
import { createProject, freshGreenRepo } from './helpers';

/** Open a project skill's panel from its tile on the Skills page (the page shows tiles; the panel opens below the grid). */
async function openSkill(page: Page, name: string) {
  await page.getByTestId('nav-skills').click();
  await page.locator(`[data-testid=skill-tile][data-name=${name}]`).click();
  return page.locator(`[data-testid=skill][data-skill=${name}]`);
}

// Skills in agent creation (decision P35), against the real core: a skill is ticked when an agent is made, the Skills page
// says who gets it and changes it, the editor takes it away again, and a template keeps the skills it carries when edited.
test('skills: given when an agent is made, who gets each one shown and changed, and kept by an edited template', async ({ page }) => {
  await createProject(page, 'skills-picker', freshGreenRepo());

  // the green fixture repository has go-style (roles: Backend); accepted, it goes to every backend agent
  await page.getByTestId('nav-skills').click();
  await page.getByRole('button', { name: 'Scan the repository for skills' }).click();
  await expect(page.locator('[data-testid=skill-tile][data-name=go-style]')).toHaveAttribute('data-status', 'offered');
  let card = await openSkill(page, 'go-style');
  await card.getByRole('button', { name: 'Accept go-style' }).click();
  await expect(card).toHaveAttribute('data-status', 'accepted');
  await expect(card.getByTestId('skill-audience-text')).toHaveText('Every Backend agent');

  // a frontend agent is given it in the new-agent form; a backend agent gets it with the team
  await page.getByTestId('nav-team').click();
  const form = page.getByTestId('agent-form');
  await form.getByLabel('Agent name').fill('Ada');
  await form.getByLabel('Role', { exact: true }).fill('frontend');
  await form.getByLabel('Adapter', { exact: true }).selectOption('claude');
  const box = form.getByTestId('agent-skills').getByRole('checkbox', { name: /^go-style/ });
  await expect(form.getByTestId('agent-skills')).toContainText('Now it goes to every Backend agent.');
  await box.check();
  await form.getByRole('button', { name: 'Create agent' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: 'Ada' })).toBeVisible();
  await form.getByLabel('Agent name').fill('Bob');
  await form.getByLabel('Role', { exact: true }).fill('backend');
  await expect(box).toBeChecked();
  await expect(box).toBeDisabled();
  await form.getByRole('button', { name: 'Create agent' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: 'Bob' })).toBeVisible();

  // the Skills page names who gets it, and narrows it to chosen agents starting from those who have it
  card = await openSkill(page, 'go-style');
  await expect(card.getByTestId('skill-audience-text')).toHaveText('Every Backend agent, and Ada');
  await card.getByRole('button', { name: 'Change who gets go-style' }).click();
  const who = card.getByTestId('skill-audience');
  await who.getByRole('radio', { name: /Only the agents I choose/ }).check();
  await expect(who.getByRole('checkbox', { name: /^Ada/ })).toBeChecked();
  await expect(who.getByRole('checkbox', { name: /^Bob/ })).toBeChecked();
  await who.getByRole('checkbox', { name: /^Bob/ }).uncheck();
  await who.getByRole('button', { name: 'Save who gets go-style' }).click();
  await expect(card.getByTestId('skill-audience-text')).toHaveText('Only Ada');

  // the editor shows what Ada has and takes it away
  await page.getByTestId('nav-team').click();
  await page.getByTestId('agent').filter({ hasText: 'Ada' }).getByRole('button', { name: 'Edit Ada' }).click();
  const editor = page.getByTestId('agent-editor');
  const mine = editor.getByTestId('agent-skills').getByRole('checkbox', { name: /^go-style/ });
  await expect(mine).toBeChecked();
  await mine.uncheck();
  await editor.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByTestId('agent-editor')).toHaveCount(0);
  card = await openSkill(page, 'go-style');
  await expect(card.getByTestId('skill-audience-text')).toHaveText('Nobody: no agent is chosen yet');

  // a template carries a registry skill (one a prebuilt agent comes with) and keeps it when it is edited
  await page.getByTestId('nav-library').click();
  await page.getByRole('button', { name: 'New agent template' }).click();
  const tf = page.getByTestId('template-form');
  await tf.getByLabel('Template name').fill('UI dev');
  await tf.getByLabel('Role').fill('frontend');
  await tf.getByLabel('Add a skill').selectOption({ label: 'frontend-design (anthropic-skills), from prebuilt UI Engineer' });
  await tf.getByRole('button', { name: 'Add skill' }).click();
  await expect(tf.getByTestId('template-skills')).toContainText('anthropic-skills, pinned at');
  await tf.getByRole('button', { name: 'Create template' }).click();
  const tpl = page.getByTestId('lib-template').filter({ hasText: 'UI dev' });
  await expect(tpl.getByRole('list', { name: 'Skills it brings' })).toContainText('frontend-design');
  await tpl.getByRole('button', { name: 'Edit' }).click();
  const edit = page.getByTestId('template-form');
  await edit.getByLabel('Template name').fill('UI developer');
  await edit.getByRole('button', { name: 'Save template' }).click();
  const renamed = page.getByTestId('lib-template').filter({ hasText: 'UI developer' });
  await expect(renamed.getByRole('list', { name: 'Skills it brings' })).toContainText('frontend-design');
});
