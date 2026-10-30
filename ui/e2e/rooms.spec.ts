// Rooms against the real core (decision P130): a person mutes a channel from its menu and the Rooms rail says so; Ctrl+. in a
// direct message opens the Agent Inspector for its agent; a join rule is made and removed in the Room tab (that it adds its agent
// to the next task room whose task names a file under its folder is tested in the core, internal/core/p130_test.go).
import { expect, test } from '@playwright/test';
import { choose, createAgent, createProjectWithoutRepo, openAgentPage, openRoom, roomLink } from './helpers';

test('a room is muted and unmuted from its menu, and the Rooms rail says it is muted', async ({ page }) => {
  await createProjectWithoutRepo(page, 'mute-rooms');
  await openRoom(page, 'general');
  await expect(page.getByLabel('Messages in general')).toBeVisible();
  const menu = page.getByTestId('room-menu');
  await menu.click();
  await page.getByRole('menuitem', { name: 'Mute room' }).click();
  await expect(roomLink(page, 'general')).toHaveAttribute('data-muted', 'true');
  await expect(roomLink(page, 'general')).toHaveAccessibleName(/^general ?, muted/);
  await menu.click();
  await page.getByRole('menuitem', { name: 'Unmute room' }).click();
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

test("a join rule is made and removed in a channel's Room tab", async ({ page }) => {
  await createProjectWithoutRepo(page, 'join-rules');
  await createAgent(page, 'Sec', 'security');
  await openRoom(page, 'general');
  await page.getByTestId('room-mode').click();
  const rules = page.getByTestId('join-rules');
  await expect(rules).toBeVisible();
  await choose(rules.getByRole('combobox', { name: 'Agent for the rule' }), { label: 'Sec' });
  await rules.getByLabel('Folder (optional)').fill('auth/');
  await rules.getByRole('button', { name: 'Add rule' }).click();
  await expect(rules.getByTestId('join-rule')).toHaveText(/Sec joins as an observer: tasks naming a file under auth\//);
  await rules.getByRole('button', { name: 'Remove the rule for Sec' }).click();
  await expect(rules.getByTestId('join-rule')).toHaveCount(0);
});
