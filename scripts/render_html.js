// Renders an HTML file to a PNG using a headless Chromium via Playwright.
// Used by scripts/screenshot.sh to turn an ansi2html terminal capture into
// an image. Not meant to be run directly.
//
// Usage: node render_html.js <html-file> <png-file> <width> <height>
const { chromium } = require('playwright');

async function main() {
  const [, , htmlPath, pngPath, widthArg, heightArg] = process.argv;
  if (!htmlPath || !pngPath) {
    console.error('usage: render_html.js <html-file> <png-file> [width] [height]');
    process.exit(1);
  }
  const width = parseInt(widthArg, 10) || 900;
  const height = parseInt(heightArg, 10) || 620;

  const executablePath = process.env.PLAYWRIGHT_CHROMIUM_PATH || undefined;
  const browser = await chromium.launch({ executablePath });
  const page = await browser.newPage({ viewport: { width, height } });
  await page.goto(`file://${htmlPath}`);
  await page.screenshot({ path: pngPath, fullPage: true });
  await browser.close();
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
