// The Guide (decision P75), in the real app: reached from the top bar, searchable, linked from the pages with "Learn more", found by
// the command palette, and the old /mcp address opens Integrations.
import { expect, test } from '@playwright/test';
import { appUrl, createProjectWithoutRepo } from './helpers';

test('the Guide: a button in the top bar, a search, a topic, Learn more from a page, the palette, and /mcp opening Integrations', async ({ page }) => {
  await createProjectWithoutRepo(page, 'gocalc-guide');

  // the top bar's button opens the Guide: sections on the left, every section's topics in the body
  await page.getByTestId('nav-guide-top').click();
  await expect(page).toHaveURL(/\/guide$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Guide' })).toBeVisible();
  const topics = page.getByRole('navigation', { name: 'Guide topics' });
  for (const section of ['Start here', 'Concepts', 'How to', 'Reference', 'Safety and privacy', 'Troubleshooting']) await expect(topics.getByText(section, { exact: true })).toBeVisible();

  // search by a word of the text, not only of the title
  await page.getByLabel('Search the Guide').fill('gitleaks');
  const results = page.getByTestId('guide-results');
  await expect(results).toContainText('Scan a repository for problems');
  await results.getByRole('link', { name: 'Scan a repository for problems' }).click();
  await expect(page).toHaveURL(/\/guide\/scan$/);
  const topic = page.getByTestId('guide-topic');
  await expect(topic.getByRole('heading', { level: 2, name: 'Scan a repository for problems' })).toBeVisible();
  await expect(topic).toContainText('Drydock downloads no rules');
  await expect(topics.getByRole('link', { name: 'Scan a repository for problems' })).toHaveAttribute('aria-current', 'page');

  // a page's "Learn more" opens its topic
  await page.goto(new URL('/routing', appUrl()).toString());
  await expect(page.getByRole('heading', { level: 1, name: 'Routing' })).toBeVisible();
  await page.getByRole('link', { name: /Learn more: Model routing and its rules/ }).click();
  await expect(page).toHaveURL(/\/guide\/routing-rules$/);
  await expect(page.getByTestId('guide-topic')).toContainText(/invariants/i);

  // the command palette finds a topic by its words
  await page.keyboard.press('Control+k');
  await page.getByRole('combobox').fill('rule sets licences');
  await page.getByRole('option', { name: /Guide: Rule sets and their licences/ }).click();
  await expect(page).toHaveURL(/\/guide\/rule-licences$/);
  // a link to a website opens in a new tab and says where it goes
  const site = page.getByTestId('guide-topic').getByRole('link', { name: 'semgrep/semgrep-rules' });
  await expect(site).toHaveAttribute('target', '_blank');
  await expect(site).toHaveAttribute('href', 'https://github.com/semgrep/semgrep-rules');

  // a topic that does not exist says so
  await page.goto(new URL('/guide/nope', appUrl()).toString());
  await expect(page.getByRole('heading', { name: 'No such topic' })).toBeVisible();

  // /mcp is an old address: it opens Integrations
  await page.goto(new URL('/mcp', appUrl()).toString());
  await expect(page).toHaveURL(/\/integrations$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Integrations' })).toBeVisible();
});
