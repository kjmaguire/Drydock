// The density budget (the UI audit of 2026-10-19, docs/ui-audit-2026-10-19.md, "Keeping it fixed"). Every page of the real app on the
// real core is measured at a laptop's usual size (1366 x 768) and at 1024 wide, and held to what the audit set:
//   - no paragraph over 200 characters on a page (the detail belongs in the Guide), no subtitle over 140 characters;
//   - text only at the sizes of the type scale (12, 12.5, 13, 15, 18, 22 px: graphite, P97) and the weights 400, 500, 600 and 700;
//   - no two neighbouring texts in a row closer than 8 px;
//   - no control smaller than 28 px high and 24 px wide (the label of a box or a switch is its target);
//   - no text said more than twice on a page outside a table or a list;
//   - nothing wider than the window at 1024 px;
//   - no two controls overlapping, and a button beside a dropdown or a text box centred on it (2026-10-20).
// A page that has a reason to break one lists it in EXEMPT with the reason.
import { expect, test, type Page } from '@playwright/test';
import { appUrl, choose, createAgent, createProject, openNewAgent, projectNav } from './helpers';

const ROUTES = [
  '/', '/needs-you', '/rooms', '/tasks', '/tasks/new', '/pulls', '/team', '/crew', '/activity', '/project', '/project/integrations', '/plan', '/requirements',
  '/milestones', '/memory', '/skills', '/packs', '/integrations', '/budgets', '/evals', '/security', '/repo-map', '/knowledge', '/home', '/chats',
  '/library', '/library/skills', '/connections', '/agents', '/agents/claude', '/github', '/security-scan', '/models', '/models/new', '/routing', '/backups', '/data', '/support', '/license',
  '/settings', '/guide', '/guide/scan',
];

/** What a page may break, and why. A text is matched by its start. */
const EXEMPT: { rule: 'paragraph' | 'repeat'; text: RegExp; why: string }[] = [
  { rule: 'paragraph', text: /^no container engine is reachable/i, why: 'the container engine\'s own error, shown as the system said it' },
];

interface Finding {
  rule: string;
  where: string;
}

async function measure(page: Page): Promise<{ findings: Finding[]; overflowX: boolean }> {
  const raw = await page.evaluate(() => {
    const SKIP = '[aria-hidden="true"], .sr-only, script, style, svg, .xterm, .monaco-editor, pre, code, kbd, [data-density-exempt]';
    const visible = (el: Element) => {
      const r = el.getBoundingClientRect();
      const s = getComputedStyle(el);
      return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none';
    };
    // text inside a closed <details> is not on the page until it is opened
    const hidden = (el: Element) => {
      const d = el.closest('details:not([open])');
      return !!d && !(el.closest('summary') && el.closest('summary')!.parentElement === d);
    };
    const own = (el: Element) =>
      Array.from(el.childNodes)
        .filter((n) => n.nodeType === 3 && n.textContent!.trim())
        .map((n) => n.textContent!.trim())
        .join(' ');
    const label = (el: Element) => `${el.tagName.toLowerCase()}${el.getAttribute('data-testid') ? `[${el.getAttribute('data-testid')}]` : ''}`;
    const main = document.querySelector('#main') ?? document.body;
    const out = { long: [] as string[], sizes: [] as string[], tight: [] as string[], small: [] as string[], repeats: [] as string[], subtitle: [] as string[] };
    const texts = new Map<string, number>();
    const inGuide = !!main.querySelector('[data-testid="guide-topic"]');
    for (const el of Array.from(main.querySelectorAll('*'))) {
      if (el.closest(SKIP) || !visible(el) || hidden(el)) continue;
      const o = own(el);
      if (!o) continue;
      const s = getComputedStyle(el);
      const size = parseFloat(s.fontSize);
      const weight = s.fontWeight;
      if (![12, 12.5, 13, 15, 18, 22].includes(size) || !['400', '500', '600', '700'].includes(weight)) out.sizes.push(`${label(el)} ${size}px/${weight} "${o.slice(0, 40)}"`);
      if (o.length > 200 && !inGuide && /^(p|div|span|li|small|label)$/i.test(el.tagName)) out.long.push(`${o.length}: ${o.slice(0, 90)}`);
      const norm = o.replace(/\s+/g, ' ').toLowerCase();
      if (norm.length >= 12 && !el.closest('table, ul, ol, dl, [role=list], [role=table], nav, h1, h2, h3, h4, a, button, label, summary, [role=tab]')) texts.set(norm, (texts.get(norm) ?? 0) + 1);
    }
    for (const [k, n] of texts) if (n > 2) out.repeats.push(`${n}x ${k.slice(0, 80)}`);
    // the subtitle under a page's title
    for (const h of Array.from(main.querySelectorAll('h1'))) {
      const sub = h.nextElementSibling as HTMLElement | null;
      if (sub && (sub.innerText ?? '').trim().length > 140) out.subtitle.push(`${(sub.innerText ?? '').trim().length}: ${(sub.innerText ?? '').trim().slice(0, 80)}`);
    }
    // text that touches the next text in a row
    // the text an editor or a terminal draws is not the element's: they draw their lines in their own clipped viewport (on the
    // Windows runner, whose column is narrower, a clipped line of the task page's diff ran under the properties beside it)
    const DRAWN = '.monaco-editor, .xterm';
    const textBox = (el: Element) => {
      const rects: DOMRect[] = [];
      const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        if (!n.textContent?.trim() || n.parentElement?.closest(DRAWN)) continue;
        const rg = document.createRange();
        rg.selectNodeContents(n);
        rects.push(...Array.from(rg.getClientRects()).filter((r) => r.width > 0 && r.height > 0));
      }
      if (!rects.length) return null;
      return { left: Math.min(...rects.map((r) => r.left)), right: Math.max(...rects.map((r) => r.right)), top: Math.min(...rects.map((r) => r.top)), bottom: Math.max(...rects.map((r) => r.bottom)) };
    };
    for (const parent of Array.from(main.querySelectorAll('*'))) {
      // the plan's steps are joined by a drawn line, the workflow canvas draws its own labels, and a page of the Guide is running prose
      if (parent.closest(SKIP) || !visible(parent) || hidden(parent) || parent.matches('[aria-label="Plan progress"], [data-testid="wf-canvas"]') || parent.closest('[data-testid="guide-topic"]')) continue;
      const kids = Array.from(parent.children).filter((c) => visible(c) && !c.closest(SKIP) && (c as HTMLElement).innerText?.trim());
      if (kids.length < 2) continue;
      const boxes = kids.map((c) => ({ c, b: textBox(c) })).filter((x) => x.b) as { c: Element; b: NonNullable<ReturnType<typeof textBox>> }[];
      boxes.sort((a, b) => a.b.left - b.b.left);
      for (let i = 0; i + 1 < boxes.length; i++) {
        const a = boxes[i]!, b = boxes[i + 1]!;
        // the workflow canvas draws labels that run to its own edge
        if (a.c.matches('[data-testid="wf-canvas"]') || b.c.matches('[data-testid="wf-canvas"]')) continue;
        const sameRow = Math.min(a.b.bottom, b.b.bottom) - Math.max(a.b.top, b.b.top) > 6;
        const gap = b.b.left - a.b.right;
        if (sameRow && gap >= -1 && gap < 8) {
          const ws = a.c.nextSibling && a.c.nextSibling.nodeType === 3 && /\s/.test(a.c.nextSibling.textContent ?? '');
          if (gap < 3 && ws) continue; // a word space in running text
          out.tight.push(`${gap.toFixed(1)}px: ${label(a.c)} "${(a.c as HTMLElement).innerText.trim().slice(0, 28)}" | ${label(b.c)} "${(b.c as HTMLElement).innerText.trim().slice(0, 28)}"`);
        }
      }
    }
    // controls: a box or a switch inside a label is hit through the label
    for (const el of Array.from(main.querySelectorAll('button, a[href], input:not([type=hidden]), select, [role=button], [role=tab], [role=switch], [role=checkbox]'))) {
      if (el.closest(SKIP) || !visible(el) || hidden(el)) continue;
      // a link in running text is as high as its line
      const parentText = Array.from(el.parentElement?.childNodes ?? [])
        .filter((n) => n.nodeType === 3)
        .map((n) => n.textContent ?? '')
        .join('')
        .trim();
      if (el.tagName === 'A' && parentText.length > 3) continue;
      // a box or a switch is hit through its label too (around it, or named by for=): the bigger of the two counts
      const named = el.id ? document.querySelector(`label[for="${el.id}"]`) : null;
      const outer = (el.closest('label') as HTMLElement | null) ?? (named as HTMLElement | null);
      const r0 = el.getBoundingClientRect();
      const r1 = outer?.getBoundingClientRect();
      const r = { width: Math.max(r0.width, r1?.width ?? 0), height: Math.max(r0.height, r1?.height ?? 0) };
      if (r.height < 28 - 0.5 || r.width < 24) out.small.push(`${label(el)} ${Math.round(r.width)}x${Math.round(r.height)} "${((el as HTMLElement).innerText || el.getAttribute('aria-label') || '').trim().slice(0, 30)}"`);
    }
    // nothing sticks out of the card it is in (a long branch name ran past its card's edge)
    const sticks: string[] = [];
    for (const card of Array.from(main.querySelectorAll('*'))) {
      const cs = getComputedStyle(card);
      if (!visible(card) || hidden(card) || parseFloat(cs.borderTopWidth) < 1 || parseFloat(cs.borderTopLeftRadius) < 4 || /hidden|auto|scroll|clip/.test(cs.overflowX)) continue;
      const cr = card.getBoundingClientRect();
      for (const el of Array.from(card.querySelectorAll('*'))) {
        if (el.closest(SKIP) || !visible(el) || hidden(el)) continue;
        const ps = getComputedStyle(el).position;
        if (ps === 'absolute' || ps === 'fixed') continue;
        // a box inside its own scroller or clipped box is contained there
        let contained = false;
        for (let a: Element | null = el.parentElement; a && a !== card; a = a.parentElement) {
          if (/hidden|auto|scroll|clip/.test(getComputedStyle(a).overflowX)) {
            contained = true;
            break;
          }
        }
        if (contained) continue;
        const r = el.getBoundingClientRect();
        if (r.right > cr.right + 1 && own(el)) sticks.push(`${label(el)} "${own(el).slice(0, 30)}" ends ${Math.round(r.right - cr.right)}px past ${label(card)}`);
      }
    }
    // no two controls overlap (a dropdown grew over the field beside it in the provider form), and a button in a row with a
    // dropdown or a text box is centred on it, not on the label above it (the owner's report of 2026-10-20)
    const CONTROL = 'button, [role=combobox], input:not([type=hidden]):not([type=checkbox]):not([type=radio]), select, textarea';
    const controls = Array.from(main.querySelectorAll(CONTROL)).filter((el) => !el.closest(SKIP) && visible(el) && !hidden(el));
    const overlap: string[] = [];
    const name = (el: Element) => `${label(el)} "${((el as HTMLElement).innerText || el.getAttribute('aria-label') || el.getAttribute('placeholder') || '').trim().slice(0, 30)}"`;
    for (let i = 0; i < controls.length; i++) {
      const a = controls[i]!.getBoundingClientRect();
      for (let j = i + 1; j < controls.length; j++) {
        const bEl = controls[j]!;
        if (controls[i]!.contains(bEl) || bEl.contains(controls[i]!)) continue;
        const b = bEl.getBoundingClientRect();
        const w = Math.min(a.right, b.right) - Math.max(a.left, b.left);
        const h = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
        if (w > 1 && h > 1) overlap.push(`${name(controls[i]!)} overlaps ${name(bEl)} by ${Math.round(w)}x${Math.round(h)}`);
      }
    }
    const misaligned: string[] = [];
    for (const row of Array.from(main.querySelectorAll('*'))) {
      const cs = getComputedStyle(row);
      if (!/flex/.test(cs.display) || !/row/.test(cs.flexDirection) || row.closest(SKIP) || !visible(row) || hidden(row)) continue;
      const kids = Array.from(row.children).filter((k) => visible(k));
      const fields = kids
        .map((k) => (k.matches(CONTROL) && !k.matches('button') ? k : k.matches('label, div') ? k.querySelector(':scope > [role=combobox], :scope > input:not([type=checkbox]):not([type=radio]), :scope > select') : null))
        .filter((f): f is Element => !!f);
      const buttons = kids.filter((k) => k.matches('button:not([role=combobox])'));
      for (const f of fields) {
        const fr = f.getBoundingClientRect();
        for (const b of buttons) {
          const br = b.getBoundingClientRect();
          if (Math.min(fr.bottom, br.bottom) - Math.max(fr.top, br.top) <= 0 && Math.abs(br.top - fr.top) > fr.height * 2) continue; // another line
          const off = Math.abs((fr.top + fr.bottom) / 2 - (br.top + br.bottom) / 2);
          if (off > 3) misaligned.push(`${name(b)} sits ${Math.round(off)}px off the middle of ${name(f)}`);
        }
      }
    }
    return { ...out, sticks: sticks.slice(0, 10), overlap: overlap.slice(0, 10), misaligned: misaligned.slice(0, 10), overflowX: document.documentElement.scrollWidth > innerWidth + 1 };
  });
  const findings: Finding[] = [];
  const add = (rule: 'paragraph' | 'repeat' | 'size' | 'tight' | 'target' | 'subtitle', list: string[]) => {
    for (const w of list) {
      if ((rule === 'paragraph' || rule === 'repeat') && EXEMPT.some((e) => e.rule === rule && e.text.test(w.replace(/^\d+x? ?:? ?/, '')))) continue;
      findings.push({ rule, where: w });
    }
  };
  add('paragraph', raw.long);
  add('size', raw.sizes);
  add('tight', raw.tight);
  add('target', raw.small);
  add('repeat', raw.repeats);
  add('subtitle', raw.subtitle);
  for (const w of raw.sticks) findings.push({ rule: 'sticks-out', where: w });
  for (const w of raw.overlap) findings.push({ rule: 'overlap', where: w });
  for (const w of raw.misaligned) findings.push({ rule: 'misaligned', where: w });
  return { findings, overflowX: raw.overflowX };
}

test('every page keeps to the density budget: short text, the type scale, room between neighbours, targets of 28 px, nothing said three times', async ({ page }) => {
  test.setTimeout(900_000);
  await page.setViewportSize({ width: 1366, height: 768 });
  await createProject(page, 'gocalc-density');
  await createAgent(page, 'Worker One', 'backend');
  await openNewAgent(page);
  await page.getByLabel('Agent name').fill('Reviewer One');
  await page.getByLabel('Role', { exact: true }).fill('reviewer');
  await choose(page.getByLabel('Adapter', { exact: true }), 'claude');
  await page.getByRole('button', { name: 'Create agent' }).click();
  await expect(page.getByTestId('agent').filter({ hasText: 'Reviewer One' })).toBeVisible();
  // a task that waits for approval, so the task pages show their busiest state
  await projectNav(page, 'nav-tasks');
  await page.getByRole('link', { name: 'New task' }).click();
  await page.getByLabel('Task title').fill('Fix Subtract');
  await page.getByLabel('Description').fill('Subtract returns 0; make it return a - b.');
  await page.getByLabel('Acceptance criteria').fill('Given 5 and 3 When Subtract is called Then 2');
  await page.getByRole('button', { name: 'Create task' }).click();
  await expect(page.getByRole('heading', { level: 1, name: /Fix Subtract/ })).toBeVisible();
  const taskUrl = new URL(page.url());
  await choose(page.getByLabel('Worker', { exact: true }), { label: 'Worker One (backend)' });
  await choose(page.getByLabel('Reviewer', { exact: true }), { label: 'Reviewer One (reviewer)' });
  await page.getByRole('button', { name: 'Assign' }).click();
  await page.getByRole('button', { name: 'Start', exact: true }).click();
  await expect(page.getByText('Needs your decision')).toBeVisible({ timeout: 120_000 });

  const base = new URL(appUrl());
  const pages = [...ROUTES, taskUrl.pathname, `${taskUrl.pathname}/run`];
  const problems: string[] = [];
  for (const path of pages) {
    const u = new URL(base.toString());
    u.pathname = path;
    await page.goto(u.toString());
    await expect(page.locator('#main h1, #main [role=heading]').first()).toBeVisible();
    await page.waitForTimeout(800);
    // the provider form is the detail of /models/new; measure it open (its dropdowns once overlapped the fields beside them)
    if (path === '/models/new') await expect(page.getByRole('form', { name: 'Add a provider' })).toBeVisible();
    await page.setViewportSize({ width: 1366, height: 768 });
    const wide = await measure(page);
    for (const f of wide.findings) problems.push(`${path} [${f.rule}] ${f.where}`);
    // a reading column starts under the title, not centred somewhere else (A1)
    if (path === '/project' || path === '/settings') {
      const edges = await page.evaluate(() => {
        const h1 = document.querySelector('#main h1')!.getBoundingClientRect().left;
        const first = document.querySelector('#main section[aria-label]')?.getBoundingClientRect().left ?? h1;
        return { h1, first };
      });
      if (Math.abs(edges.h1 - edges.first) > 2) problems.push(`${path} [frame] the content starts at x=${Math.round(edges.first)}, the title at x=${Math.round(edges.h1)}`);
    }
    await page.setViewportSize({ width: 1024, height: 700 });
    await page.waitForTimeout(250);
    const narrow = await measure(page);
    if (narrow.overflowX) problems.push(`${path} [overflow] wider than the window at 1024 px`);
  }
  expect(problems, `${problems.length} things over budget:\n${problems.join('\n')}`).toEqual([]);
});
