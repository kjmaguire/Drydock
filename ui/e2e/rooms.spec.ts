// Rooms against the real core (decision P130): a person mutes a channel from its menu and the Rooms rail says so; Ctrl+. in a
// direct message opens the Agent Inspector for its agent; a join rule is made and removed in the Room tab (that it adds its agent
// to the next task room whose task names a file under its folder is tested in the core, internal/core/p130_test.go).
import { expect, test, type Locator } from '@playwright/test';
import { choose, createAgent, createProjectWithoutRepo, openAgentPage, openRoom, projectNav, roomLink } from './helpers';

/** Open a kit menu by its trigger (repeated while it is still closing after the last choice) and choose an item. */
async function fromMenu(trigger: Locator, item: Locator): Promise<void> {
  await expect(async () => {
    if (!(await item.isVisible())) await trigger.click();
    await item.click({ timeout: 2_000 });
  }).toPass({ timeout: 20_000 });
}

test('a room is muted and unmuted with one click on its bell, and the Rooms rail says it is muted', async ({ page }) => {
  await createProjectWithoutRepo(page, 'mute-rooms');
  await openRoom(page, 'general');
  await expect(page.getByLabel('Messages in general')).toBeVisible();
  const bell = page.getByTestId('room-mute');
  await expect(bell).toHaveAccessibleName('Mute room');
  await bell.click();
  await expect(roomLink(page, 'general')).toHaveAttribute('data-muted', 'true');
  await expect(roomLink(page, 'general')).toHaveAccessibleName(/^general ?, muted/);
  await expect(bell).toHaveAccessibleName('Unmute room');
  await bell.click();
  await expect(roomLink(page, 'general')).not.toHaveAttribute('data-muted', 'true');
});

test("Ctrl+. in a direct message opens the agent's inspector", async ({ page }) => {
  await createProjectWithoutRepo(page, 'dm-inspect');
  await createAgent(page, 'Ida', 'backend');
  await openAgentPage(page, 'Ida');
  await page.getByRole('button', { name: 'Message Ida' }).click();
  await expect(page.getByLabel('Messages in dm:', { exact: false })).toBeVisible();
  await page.getByLabel('Messages in dm:', { exact: false }).click();
  await page.keyboard.press('Control+.');
  await expect(page.getByTestId('inspector')).toBeVisible();
  await expect(page.getByTestId('inspector')).toContainText('Ida');
});

test("a join rule is set on the agent's editor and said on a channel's Room tab (X17)", async ({ page }) => {
  await createProjectWithoutRepo(page, 'join-rules');
  await createAgent(page, 'Sec', 'security');
  await openRoom(page, 'general');
  await page.getByTestId('room-mode').click();
  const rules = page.getByTestId('join-rules');
  await expect(rules.getByTestId('join-rules-none')).toBeVisible();
  // the rule lives on the crew: the agent's editor, under More settings
  await projectNav(page, 'nav-overview');
  await page.getByRole('complementary', { name: 'Crew' }).locator('[data-testid="work-agent"][data-name="Sec"]').click();
  const editor = page.getByTestId('agent-editor');
  await editor.getByTestId('agent-more-settings').locator(':scope > summary').click();
  await choose(editor.getByRole('combobox', { name: 'Joins task rooms' }), 'every');
  await expect(editor.getByRole('combobox', { name: 'Joins task rooms' })).toHaveAttribute('data-value', 'every');
  await expect(editor.getByRole('combobox', { name: 'Joins task rooms' })).toBeEnabled();
  await page.keyboard.press('Escape');
  await openRoom(page, 'general');
  await page.getByTestId('room-mode').click();
  await expect(page.getByTestId('join-rules').getByTestId('join-rule')).toContainText('Sec joins every task room as an observer');
  await page.getByTestId('join-rules').getByRole('link', { name: 'Change the join rule of Sec' }).click();
  const again = page.getByTestId('agent-editor');
  await again.getByTestId('agent-more-settings').locator(':scope > summary').click();
  await choose(again.getByRole('combobox', { name: 'Joins task rooms' }), 'never');
  await expect(again.getByRole('combobox', { name: 'Joins task rooms' })).toHaveAttribute('data-value', 'never');
});
