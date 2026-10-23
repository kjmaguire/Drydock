import { expect, test } from '@playwright/test';
import { cssString } from './helpers';

// The helpers find an option by its value with an attribute selector; a value with a backslash, a quote or a line break must
// still match exactly its own option, in the browser's own selector engine.
test('cssString quotes any value for an attribute selector', async ({ page }) => {
  const values = ['plain', 'say "hi"', 'C:\\Users\\me', 'ends with \\', '\\"', 'two\nlines', 'cr\rff\f', "it's"];
  await page.setContent('<ul></ul>');
  await page.evaluate((vs) => {
    for (const v of vs) {
      const li = document.createElement('li');
      li.setAttribute('data-value', v);
      li.textContent = JSON.stringify(v);
      document.querySelector('ul')!.append(li);
    }
  }, values);
  for (const v of values) {
    const hit = page.locator(`li[data-value=${cssString(v)}]`);
    await expect(hit, v).toHaveCount(1);
    await expect(hit, v).toHaveText(JSON.stringify(v));
  }
});
