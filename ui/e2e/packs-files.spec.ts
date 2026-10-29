// A pack made from the person's own files (decision P110) against the real core: the page sends the files, the core reads
// the PDF with its page numbers and the Markdown, search cites the PDF's page, a question with no model set up is answered
// with the cited passages and says so, and files are added and removed from the pack's card. The PDF is written here byte
// by byte (a TEST FIXTURE, like internal/docparse/docfixture), not taken from a real producer.
import { expect, test } from '@playwright/test';
import { createProjectWithoutRepo, projectNav } from './helpers';

/** A valid PDF with one page per entry, each a list of lines of text (Helvetica, with widths). */
function pdf(title: string, pages: string[][]): Buffer {
  const objs: string[] = [];
  const kids = pages.map((_, i) => `${4 + 2 * i} 0 R`).join(' ');
  objs.push('<< /Type /Catalog /Pages 2 0 R >>');
  objs.push(`<< /Type /Pages /Kids [${kids}] /Count ${pages.length} >>`);
  objs.push(`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding /FirstChar 32 /LastChar 126 /Widths [${Array(95).fill('500').join(' ')}] >>`);
  pages.forEach((lines, i) => {
    const stream = `BT /F1 12 Tf 72 720 Td ${lines.map((l, j) => `${j ? '0 -14 Td ' : ''}(${l}) Tj`).join(' ')} ET`;
    objs.push(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents ${5 + 2 * i} 0 R >>`);
    objs.push(`<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`);
  });
  objs.push(`<< /Title (${title}) >>`);
  let out = '%PDF-1.4\n';
  const offsets: number[] = [];
  objs.forEach((o, i) => {
    offsets.push(out.length);
    out += `${i + 1} 0 obj\n${o}\nendobj\n`;
  });
  const xref = out.length;
  out += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n${offsets.map((o) => `${String(o).padStart(10, '0')} 00000 n \n`).join('')}`;
  out += `trailer\n<< /Size ${objs.length + 1} /Root 1 0 R /Info ${objs.length} 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(out, 'latin1');
}

test('a pack made from a PDF and a note: page citations, an answer without a model, files added and removed', async ({ page }) => {
  await createProjectWithoutRepo(page, 'packs-files');
  await projectNav(page, 'nav-packs');
  const form = page.getByTestId('files-pack-form');
  await expect(form).toBeVisible();
  await form.getByLabel('Title').fill('Operator manual');
  await expect(form.getByLabel('Pack id')).toHaveValue('operator-manual');
  await form.getByLabel('Files').setInputFiles([
    {
      name: 'manual.pdf',
      mimeType: 'application/pdf',
      buffer: pdf('Operator Manual', [
        ['Starting the gateway', 'Run the gateway with the service account, never as root.'],
        ['Rotating credentials', 'Rotate the gateway token every ninety days with rotate-token.'],
      ]),
    },
    { name: 'notes.md', mimeType: 'text/markdown', buffer: Buffer.from('# On-call notes\n\nPage the duty engineer through the escalation bridge.\n') },
  ]);
  await form.getByRole('button', { name: 'Make the pack' }).click();
  await expect(form.getByRole('status')).toContainText('Made operator-manual. Version');
  await expect(form.getByRole('status')).toContainText('2 added');
  const card = page.locator('[data-testid=pack][data-pack=operator-manual]');
  await expect(card).toBeVisible();
  await expect(card.getByText('private', { exact: true })).toBeVisible();
  await expect(card.getByTestId('pack-file')).toHaveCount(2);

  // search cites the PDF's page
  await page.getByLabel('Search packs').fill('rotate gateway token');
  await page.getByRole('button', { name: 'Search', exact: true }).click();
  const hit = page.getByTestId('pack-hit').first();
  await expect(hit.getByTestId('citation')).toContainText('Operator Manual > Page 2');
  await expect(hit.getByTestId('citation')).toHaveAttribute('data-page', '2');

  // no model is set up in this harness: the answer is the passages, and the page says why
  await page.getByLabel('Ask the packs').fill('how often is the gateway token rotated?');
  await page.getByRole('button', { name: 'Consult' }).click();
  const consult = page.getByTestId('pack-consult');
  await expect(consult).toContainText('No model is set up for questions');
  await expect(consult.getByTestId('consult-no-model')).toBeVisible();
  await expect(consult).toContainText('Page 2');

  // add a file from the card, then remove the PDF: each change is read at once
  await card.getByTestId('pack-file-input').setInputFiles({ name: 'faq.txt', mimeType: 'text/plain', buffer: Buffer.from('Where are the logs? In the logs folder.\n') });
  await expect(card.getByTestId('pack-files').getByRole('status')).toContainText('Added faq.txt.');
  await expect(card.getByTestId('pack-file')).toHaveCount(3);
  await card.getByRole('button', { name: 'Remove manual.pdf' }).click();
  await card.getByRole('button', { name: 'Yes, remove manual.pdf' }).click();
  await expect(card.getByTestId('pack-files').getByRole('status')).toContainText('Removed manual.pdf.');
  await expect(card.getByTestId('pack-file')).toHaveCount(2);
  await page.getByLabel('Search packs').fill('rotate gateway token');
  await page.getByRole('button', { name: 'Search', exact: true }).click();
  await expect(page.getByTestId('pack-search-result')).toHaveAttribute('data-found', 'false');
});
