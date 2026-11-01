import { expect, test, type Page } from '@playwright/test';
import { choose, createProject, freshGreenRepo, openAgentPage, openNewAgent, workspaceNav, projectNav } from './helpers';

/** Open a project skill's panel from its row on the Skills page (one-line rows; the panel opens inside the row). */
async function openSkill(page: Page, name: string) {
  await projectNav(page, 'nav-skills');
  const row = page.locator(`[data-testid=skill-tile][data-name=${name}]`);
  await row.locator('button.what').click();
  return row.locator(`[data-testid=skill][data-skill=${name}]`);
}

// Skills in agent creation (decision P35), against the real core: a skill is ticked when an agent is made, the Skills page
// says who gets it and changes it, the editor takes it away again, and a template keeps the skills it carries when edited.
test('skills: given when an agent is made, who gets each one shown and changed, and kept by an edited template', async ({ page }) => {
  await createProject(page, 'skills-picker', freshGreenRepo());

  // the green fixture repository has go-style (roles: Backend); accepted with no agent on the team, it goes to nobody yet (P76)
  await projectNav(page, 'nav-skills');
  await page.getByRole('button', { name: 'Scan the repository for skills' }).click();
  await expect(page.locator('[data-testid=skill-tile][data-name=go-style]')).toHaveAttribute('data-status', 'offered');
  let card = await openSkill(page, 'go-style');
  await card.getByRole('button', { name: 'Accept go-style' }).click();
  await expect(card).toHaveAttribute('data-status', 'accepted');
  await expect(card.getByTestId('skill-audience-text')).toHaveText('Nobody yet: give it to the agents that need it');

  // a frontend agent is given it in the new-agent form; a backend agent is only offered it as a suggestion, never given it by role
  await openNewAgent(page);
  const form = page.getByTestId('agent-form');
  await form.getByLabel('Agent name').fill('Ada');
  await form.getByLabel('Role', { exact: true }).fill('frontend');
  await choose(form.getByLabel('Adapter', { exact: true }), 'claude');
  const box = form.getByTestId('agent-skills').getByRole('checkbox', { name: /^go-style/ });
  await expect(form.getByTestId('agent-skills')).toContainText('Not given to another agent.');
  await box.check();
  await form.getByRole('button', { name: 'Create agent' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: 'Ada' })).toBeVisible();
  // the dialog closed with the agent made; New agent opens a fresh form
  await openNewAgent(page, false);
  await form.getByLabel('Agent name').fill('Bob');
  await form.getByLabel('Role', { exact: true }).fill('backend');
  await expect(form.getByTestId('agent-skills')).toContainText('Suggested for backend. Also given to Ada.');
  await expect(box).not.toBeChecked();
  await expect(box).toBeEnabled();
  await form.getByRole('button', { name: 'Create agent' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: 'Bob' })).toBeVisible();

  // the Skills page names who gets it, and changes it starting from those who have it: Bob given it, Ada's taken away
  card = await openSkill(page, 'go-style');
  await expect(card.getByTestId('skill-audience-text')).toHaveText('Ada');
  await card.getByRole('button', { name: 'Change who gets go-style' }).click();
  const who = card.getByTestId('skill-audience');
  await expect(who.getByRole('radio')).toHaveCount(0);
  await expect(who.getByRole('checkbox', { name: /^Ada/ })).toBeChecked();
  await expect(who.getByRole('checkbox', { name: /^Bob/ })).not.toBeChecked();
  await who.getByRole('checkbox', { name: /^Bob/ }).check();
  await who.getByRole('checkbox', { name: /^Ada/ }).uncheck();
  await who.getByRole('button', { name: 'Save who gets go-style' }).click();
  await expect(card.getByTestId('skill-audience-text')).toHaveText('Bob');

  // the editor shows what Bob has and takes it away
  await openAgentPage(page, 'Bob');
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  const editor = page.getByTestId('agent-editor');
  // the panel shows it as a chip; its x takes it away
  await expect(editor.getByTestId('skill-chip').filter({ hasText: 'go-style' })).toBeVisible();
  await editor.getByRole('button', { name: 'Take go-style away' }).click();
  await expect(editor.getByTestId('skill-chip')).toHaveCount(0);
  await editor.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByTestId('agent-editor')).toHaveCount(0);
  card = await openSkill(page, 'go-style');
  await expect(card.getByTestId('skill-audience-text')).toHaveText('Nobody yet: give it to the agents that need it');

  // a template carries a registry skill (one a prebuilt agent comes with) and keeps it when it is edited
  await workspaceNav(page, 'nav-library');
  await page.getByRole('button', { name: 'New agent template' }).click();
  const tf = page.getByTestId('template-form');
  await tf.getByLabel('Template name').fill('UI dev');
  await tf.getByLabel('Role').fill('frontend');
  await choose(tf.getByLabel('Add a skill'), { label: 'frontend-design (anthropic-skills), from prebuilt UI Engineer' });
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
