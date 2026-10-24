// Regenerates build/windows/icon.ico and build/windows/icon-1024.png from Drydock's mark (docs/windows-identity.md).
//
//   node build/windows/make-icon.mjs [--preview <dir>]
//
// The mark is ui/public/favicon.svg. At 16 and 20 pixels its 2.2 stroke (on a 32 grid) is a blur, so those two sizes are drawn
// from their own simplified sources here (icon-16.svg, icon-20.svg: the same ring, shank and arms, thicker and on the pixel
// grid). Each size is rasterized by Chromium (the one Playwright in ui/ uses; `npm ci` in ui/ first) straight from the vector,
// never scaled from another bitmap. The .ico holds 16, 20, 24, 32, 40, 48, 64, 96 and 128 as 32-bit BMP entries and 256 as a
// PNG entry, the layout Windows expects. --preview also writes every size as a PNG, to look at.
import { createRequire } from 'node:module';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '..', '..');
const require = createRequire(join(root, 'ui', 'package.json'));
const { chromium } = require('playwright');

export const SIZES = [16, 20, 24, 32, 40, 48, 64, 96, 128, 256];
const MASTER = join(root, 'ui', 'public', 'favicon.svg');
const SOURCE = { 16: join(here, 'icon-16.svg'), 20: join(here, 'icon-20.svg') };

// The Chromium Playwright wants may not be the one installed (a sandbox ships its own under PLAYWRIGHT_BROWSERS_PATH); then
// the newest chromium-* there is used, as ui/playwright.config.ts does. CHROMIUM names one outright.
function chromiumPath() {
  if (process.env.CHROMIUM) return process.env.CHROMIUM;
  if (existsSync(chromium.executablePath())) return undefined;
  const base = process.env.PLAYWRIGHT_BROWSERS_PATH;
  if (!base || !existsSync(base)) return undefined;
  for (const d of readdirSync(base).filter((d) => /^chromium-\d+$/.test(d)).sort().reverse()) {
    for (const rel of ['chrome-linux/chrome', 'chrome-linux64/chrome', 'chrome-win/chrome.exe', 'chrome-win64/chrome.exe']) {
      if (existsSync(join(base, d, rel))) return join(base, d, rel);
    }
  }
  return undefined;
}

// render draws the SVG at size x size in a canvas and returns its straight (not premultiplied) RGBA pixels and a PNG.
async function render(page, svg, size) {
  const url = 'data:image/svg+xml;base64,' + Buffer.from(svg).toString('base64');
  const out = await page.evaluate(
    async ({ url, size }) => {
      const img = new Image(size, size);
      img.src = url;
      await img.decode();
      const c = document.createElement('canvas');
      c.width = size;
      c.height = size;
      const g = c.getContext('2d', { colorSpace: 'srgb' });
      g.drawImage(img, 0, 0, size, size);
      const rgba = Array.from(g.getImageData(0, 0, size, size).data);
      return { rgba, png: c.toDataURL('image/png').split(',')[1] };
    },
    { url, size },
  );
  return { rgba: Buffer.from(out.rgba), png: Buffer.from(out.png, 'base64') };
}

// bmpEntry is an icon image as Windows stores it: a BITMAPINFOHEADER with twice the height, bottom-up BGRA rows, then the
// 1-bit AND mask (set where the pixel is fully transparent), each mask row padded to 4 bytes.
function bmpEntry(rgba, size) {
  const header = Buffer.alloc(40);
  header.writeUInt32LE(40, 0);
  header.writeInt32LE(size, 4);
  header.writeInt32LE(size * 2, 8);
  header.writeUInt16LE(1, 12);
  header.writeUInt16LE(32, 14);
  header.writeUInt32LE(0, 16); // BI_RGB
  const maskStride = Math.ceil(size / 32) * 4;
  header.writeUInt32LE(size * size * 4 + maskStride * size, 20);
  const pixels = Buffer.alloc(size * size * 4);
  const mask = Buffer.alloc(maskStride * size);
  for (let y = 0; y < size; y++) {
    const row = size - 1 - y;
    for (let x = 0; x < size; x++) {
      const s = (y * size + x) * 4;
      const d = (row * size + x) * 4;
      pixels[d] = rgba[s + 2];
      pixels[d + 1] = rgba[s + 1];
      pixels[d + 2] = rgba[s];
      pixels[d + 3] = rgba[s + 3];
      if (rgba[s + 3] === 0) mask[row * maskStride + (x >> 3)] |= 0x80 >> (x & 7);
    }
  }
  return Buffer.concat([header, pixels, mask]);
}

function ico(entries) {
  const head = Buffer.alloc(6 + 16 * entries.length);
  head.writeUInt16LE(0, 0);
  head.writeUInt16LE(1, 2); // icon
  head.writeUInt16LE(entries.length, 4);
  let offset = head.length;
  entries.forEach(({ size, data }, i) => {
    const e = 6 + 16 * i;
    head.writeUInt8(size >= 256 ? 0 : size, e);
    head.writeUInt8(size >= 256 ? 0 : size, e + 1);
    head.writeUInt8(0, e + 2); // no palette
    head.writeUInt8(0, e + 3);
    head.writeUInt16LE(1, e + 4); // planes
    head.writeUInt16LE(32, e + 6); // bits per pixel
    head.writeUInt32LE(data.length, e + 8);
    head.writeUInt32LE(offset, e + 12);
    offset += data.length;
  });
  return Buffer.concat([head, ...entries.map((e) => e.data)]);
}

async function main() {
  const i = process.argv.indexOf('--preview');
  const preview = i > 0 ? process.argv[i + 1] : '';
  if (preview) mkdirSync(preview, { recursive: true });
  const browser = await chromium.launch({ headless: true, executablePath: chromiumPath() });
  try {
    const page = await browser.newPage();
    await page.setContent('<!doctype html><title>icon</title>');
    const entries = [];
    for (const size of SIZES) {
      const svg = readFileSync(SOURCE[size] ?? MASTER, 'utf8');
      const { rgba, png } = await render(page, svg, size);
      entries.push({ size, data: size >= 256 ? png : bmpEntry(rgba, size) });
      if (preview) writeFileSync(join(preview, `icon-${size}.png`), png);
    }
    writeFileSync(join(here, 'icon.ico'), ico(entries));
    const master = await render(page, readFileSync(MASTER, 'utf8'), 1024);
    writeFileSync(join(here, 'icon-1024.png'), master.png);
  } finally {
    await browser.close();
  }
  console.log('wrote build/windows/icon.ico (' + SIZES.join(', ') + ') and build/windows/icon-1024.png');
}

await main();
