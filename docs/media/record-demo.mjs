// SPDX-License-Identifier: MIT
//
// Makes the README's screenshots (docs/images) and demo video (docs/media).
// It drives the web UI of a running sunspec-explorerd against a device that
// "sunspecctl serve" simulates on tcp://127.0.0.1:1502, in the local Chrome.
//
//   npm i playwright                    # in a scratch directory, not in the repo
//   sunspec-explorerd --listen 127.0.0.1:18770 --data-dir /tmp/sunspec-explorer-demo --allow-target 127.0.0.0/8
//   sunspec-explorerd url --data-dir /tmp/sunspec-explorer-demo > url.txt
//   node record-demo.mjs shots out/png url.txt     # 2x screenshots
//   node record-demo.mjs video out/vid url.txt     # frames + frames.txt for ffmpeg
//   ffmpeg -f concat -safe 0 -i out/vid/frames.txt -vf fps=30,format=yuv420p -c:v libx264 -crf 23 -movflags +faststart demo.mp4
//
// Start each run with an empty data directory: recording names are unique.
import { chromium } from 'playwright';
import { readFileSync, mkdirSync } from 'node:fs';

const mode = process.argv[2] ?? 'shots';
const out = process.argv[3];
const url = readFileSync(process.argv[4], 'utf8').trim();
const target = 'tcp://127.0.0.1:1502';
mkdirSync(out, { recursive: true });

const video = mode === 'video';
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const context = await browser.newContext({
  viewport: video ? { width: 1280, height: 760 } : { width: 1440, height: 860 },
  deviceScaleFactor: video ? 1 : 2,
  colorScheme: 'dark',
  bypassCSP: video,
});
if (video) {
  // A visible pointer: recorded videos have none.
  await context.addInitScript(() => {
    addEventListener('DOMContentLoaded', () => {
      const dot = document.createElement('div');
      dot.style.cssText = 'position:fixed;z-index:2147483647;width:18px;height:18px;margin:-9px 0 0 -9px;border-radius:50%;' +
        'background:rgba(63,193,198,.35);border:2px solid #3fc1c6;pointer-events:none;transition:transform .08s;left:-40px;top:-40px';
      document.body.appendChild(dot);
      addEventListener('mousemove', (e) => { dot.style.left = e.clientX + 'px'; dot.style.top = e.clientY + 'px'; }, true);
      addEventListener('mousedown', () => { dot.style.transform = 'scale(.7)'; }, true);
      addEventListener('mouseup', () => { dot.style.transform = ''; }, true);
    });
  });
}
const page = await context.newPage();
// Video: Chrome's screencast frames with their timestamps; ffmpeg assembles them.
const frames = [];
let cdp;
if (video) {
  cdp = await context.newCDPSession(page);
  cdp.on('Page.screencastFrame', async (f) => {
    frames.push({ t: f.metadata.timestamp, data: f.data });
    await cdp.send('Page.screencastFrameAck', { sessionId: f.sessionId }).catch(() => {});
  });
}
const startRecording = async () => { if (video) await cdp.send('Page.startScreencast', { format: 'png', everyNthFrame: 1 }); };
const pause = (ms) => page.waitForTimeout(video ? ms : Math.min(ms, 600));
const shot = async (name) => { if (!video) { await page.waitForTimeout(500); await page.screenshot({ path: `${out}/${name}.png` }); console.log('shot', name); } };
const step = async (name, fn) => { try { await fn(); } catch (e) { console.log('STEP FAILED', name, String(e).split('\n')[0]); await page.screenshot({ path: `${out}/failed-${name.replace(/ /g, '-')}.png` }).catch(() => {}); } };
// Moves the pointer to an element and clicks it, visibly.
const click = async (locator) => {
  const box = await locator.boundingBox();
  if (box) { await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: video ? 25 : 1 }); await pause(250); }
  await locator.click();
};
const typeInto = async (locator, text) => { await click(locator); await locator.fill(''); await locator.pressSequentially(text, { delay: video ? 60 : 0 }); };

const models = () => page.getByRole('navigation', { name: 'Models' });
const urlField = () => page.locator('form[aria-label="Connection"] input').first();

await page.goto(url);
await page.waitForTimeout(800);
await startRecording();
await pause(1500);

if (!video) {
  // A failed connect: every probe and its error, with the usual cause.
  await step('wrong unit', async () => {
    await urlField().fill(target);
    const unit = page.locator('form[aria-label="Connection"] input[type=number]').first();
    await unit.fill('9');
    await page.getByRole('button', { name: 'Connect' }).click();
    await page.getByText(/unit ID/i).first().waitFor({ timeout: 20000 });
    await shot('connect-error');
    await unit.fill('1');
  });
}

await step('connect', async () => {
  await typeInto(urlField(), target);
  await pause(400);
  await click(page.getByRole('button', { name: 'Connect' }));
  await models().getByText(/Inverter/).first().waitFor({ timeout: 20000 });
});
await pause(2500);
await shot('overview');

await step('scroll', async () => { await page.mouse.move(800, 500); await page.mouse.wheel(0, 500); await pause(1500); await page.mouse.wheel(0, -500); await pause(800); });

await step('model 103', async () => {
  await click(models().getByRole('button', { name: /Inverter \(Three Phase\)/ }).first());
  await pause(1500);
  const w = page.getByText('W', { exact: true }).first();
  await click(w);
  await pause(2000);
});
await shot('inverter');

await step('filter', async () => {
  await typeInto(page.getByLabel('Filter points by name, label or description'), 'phase');
  await pause(2000);
  await shot('filter');
  await page.getByLabel('Filter points by name, label or description').fill('');
  await pause(500);
});

await step('model 160', async () => {
  await click(models().getByRole('button', { name: /Multiple MPPT/ }).first());
  await pause(1500);
  await page.mouse.move(800, 520);
  await page.mouse.wheel(0, 300);
  await pause(1500);
});
await shot('mppt-modules');

await step('all models', async () => { await click(models().getByRole('button', { name: 'All models' })); await pause(800); });

await step('polling', async () => {
  const interval = page.getByLabel('Poll interval in whole seconds');
  await typeInto(interval, '1');
  await click(page.getByRole('button', { name: 'Start polling' }));
  await page.waitForTimeout(3500);
});

await step('record', async () => {
  await click(page.getByRole('button', { name: 'Record', exact: true }));
  await pause(800);
  const name = page.getByRole('dialog').locator('input').first();
  await typeInto(name, 'demo-run');
  await pause(400);
  await click(page.getByRole('dialog').getByRole('button', { name: 'Start recording' }));
  await page.waitForTimeout(video ? 7000 : 6500);
});
await shot('polling-recording');

await step('stop recording', async () => {
  await click(page.getByRole('button', { name: 'Stop recording' }));
  await pause(2500);
});
await shot('recording-stopped');

await step('recordings', async () => {
  await click(page.getByRole('button', { name: 'Recordings' }));
  await pause(2500);
  await shot('recordings');
  await page.keyboard.press('Escape');
  await pause(600);
});

await step('export', async () => {
  await click(page.getByRole('button', { name: 'Export…' }));
  await pause(2500);
  await shot('export');
  await page.keyboard.press('Escape');
  await pause(600);
});

await step('light theme', async () => {
  await click(page.getByRole('button', { name: /Switch to light theme/ }));
  await pause(2500);
  await shot('light');
});

await step('collapse', async () => {
  await click(page.getByRole('button', { name: 'Hide the model list' }));
  await pause(2500);
  await shot('collapsed');
  await click(page.getByRole('button', { name: 'Show the model list' }));
  await pause(800);
  await click(page.getByRole('button', { name: /Switch to dark theme/ }));
  await pause(1500);
});

await step('stop polling', async () => { await click(page.getByRole('button', { name: 'Stop polling' })); await pause(1200); });
await step('disconnect', async () => { await click(page.getByRole('button', { name: 'Disconnect' })); await pause(1500); });

if (video) {
  await cdp.send('Page.stopScreencast');
  await page.waitForTimeout(300);
  // Frames come only when the page changes: each lasts until the next one.
  const { writeFileSync } = await import('node:fs');
  const list = [];
  frames.forEach((f, i) => {
    const name = `frame-${String(i).padStart(5, '0')}.png`;
    writeFileSync(`${out}/${name}`, Buffer.from(f.data, 'base64'));
    const next = frames[i + 1] ? frames[i + 1].t : f.t + 1.5;
    list.push(`file '${name}'`, `duration ${Math.max(0.02, next - f.t).toFixed(3)}`);
  });
  list.push(`file 'frame-${String(frames.length - 1).padStart(5, '0')}.png'`);
  writeFileSync(`${out}/frames.txt`, list.join('\n') + '\n');
  console.log('frames', frames.length, 'seconds', (frames.at(-1).t - frames[0].t + 1.5).toFixed(1));
}
await context.close();
await browser.close();
console.log('done');
