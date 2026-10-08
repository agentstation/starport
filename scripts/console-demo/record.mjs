#!/usr/bin/env node
// Record the actual Console against an isolated development gateway.
import { spawn, execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { copyFileSync, existsSync, mkdtempSync, readFileSync, readdirSync, statSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const args = process.argv.slice(2);
const names = ['--binary', '--starmap-binary', '--starmap-source', '--output', '--browser-path', '--playwright-module'];
for (let index = 0; index < args.length; index += 2) {
  if (!names.includes(args[index]) || !args[index + 1] || args[index + 1].startsWith('--')) {
    throw new Error('Use the documented recorder options with values');
  }
}
const option = (name, fallback) => {
  const at = args.indexOf(name);
  if (at >= 0 && !args[at + 1]) throw new Error(`Missing value for ${name}`);
  return at >= 0 ? args[at + 1] : fallback;
};
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const binary = path.resolve(option('--binary', path.join(root, 'starport')));
const starmapBinary = path.resolve(option('--starmap-binary', path.join(root, '../starmap-product-demos/starmap')));
const starmapSource = path.resolve(option('--starmap-source', path.join(root, '../starmap-product-demos')));
const catalogInputPaths = ['internal/embedded/catalog/generation.json', 'internal/embedded/catalog/generation-manifest.json',
  'internal/embedded/catalog/authors/openai/models/gpt-6.1-sol.yaml', 'internal/embedded/catalog/providers/openai/models/gpt-6.1-sol.yaml'];
const catalogInputs = Object.fromEntries(catalogInputPaths.map((relative) => {
  const bytes = readFileSync(path.join(starmapSource, relative));
  if (!readFileSync(starmapBinary).includes(bytes)) throw new Error(`The Starmap binary does not embed the selected source input: ${relative}`);
  return [relative, createHash('sha256').update(bytes).digest('hex')];
}));
const output = path.resolve(option('--output', path.join(root, 'docs/assets/console-demo')));
const browserPath = option('--browser-path', process.env.VHS_BROWSER_PATH);
const modulePath = option('--playwright-module', 'playwright');
const { chromium } = createRequire(import.meta.url)(modulePath);
if (!browserPath) throw new Error('--browser-path must name the Chrome executable');
if (existsSync(output) && readdirSync(output).length) throw new Error('Output directory must be empty');
const work = mkdtempSync(path.join(tmpdir(), 'starport-console-demo-'));
const capturedBinary = path.join(work, 'starport');
copyFileSync(binary, capturedBinary);
const capturedStarmap = path.join(work, 'starmap');
copyFileSync(starmapBinary, capturedStarmap);
mkdirSync(output, { recursive: true });
const profile = '(version 1)(allow default)(deny network-outbound (remote ip "*:*"))(allow network-outbound (remote ip "localhost:*"))';
const allocatePort = () => new Promise((resolve, reject) => {
  const server = createServer();
  server.on('error', reject);
  server.listen(0, '127.0.0.1', () => {
    const port = server.address().port;
    server.close(() => resolve(port));
  });
});
const port = await allocatePort();
const catalogPort = await allocatePort();
const origin = `http://127.0.0.1:${port}`;
const catalogOrigin = `http://127.0.0.1:${catalogPort}`;
const catalogServer = spawn('/usr/bin/sandbox-exec', ['-p', profile, capturedStarmap, '--quiet', 'serve', '--host', '127.0.0.1', '--port', String(catalogPort)], {
  cwd: work, env: { PATH: '/usr/bin:/bin:/usr/sbin:/sbin', HOME: work,
    STARMAP_HOME: path.join(work, 'starmap-home'), STARMAP_CATALOG_SOURCE: 'embedded',
    STARMAP_CATALOG_NETWORK_MODE: 'offline', STARMAP_CATALOG_ACQUISITION_ENABLED: 'false',
    STARMAP_CATALOG_SOURCE_POLL_INTERVAL: '0s' }, stdio: ['ignore', 'ignore', 'pipe'],
});
let catalogServerError;
catalogServer.once('error', (error) => { catalogServerError = error; });
const catalogServerExit = new Promise((resolve) => {
  catalogServer.once('exit', resolve);
  catalogServer.once('error', () => resolve(-1));
});
const environment = {
  PATH: '/usr/bin:/bin:/usr/sbin:/sbin', HOME: work,
  XDG_CONFIG_HOME: path.join(work, 'config'), XDG_DATA_HOME: path.join(work, 'data'),
  XDG_STATE_HOME: path.join(work, 'state'), STARPORT_SERVER_PORT: String(port),
  STARPORT_CATALOG_SOURCE: 'starmap', STARPORT_CATALOG_SOURCE_URL: `${catalogOrigin}/api/v1`,
  STARPORT_CATALOG_SOURCE_STARTUP_POLICY: 'require_source', STARPORT_CATALOG_ACQUISITION_ENABLED: 'false',
  STARPORT_CATALOG_ACQUISITION_SOURCES: '',
};
let gateway;
let gatewayExit;
let banner = '';
let browser;
let context;
let stopped = false;
let catalogStopped = false;
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const stopGateway = async () => {
  if (!gateway) return;
  gateway.kill('SIGINT');
  const timer = setTimeout(() => gateway.kill('SIGKILL'), 5000);
  try { return await gatewayExit; } finally { clearTimeout(timer); }
};
const stopCatalog = async () => {
  catalogServer.kill('SIGINT');
  const timer = setTimeout(() => catalogServer.kill('SIGKILL'), 5000);
  try { return await catalogServerExit; } finally { clearTimeout(timer); }
};
const hash = (file) => createHash('sha256').update(readFileSync(file)).digest('hex');
const scenes = [];
const errors = [];
try {
  let sourceReady = false;
  for (let attempt = 0; attempt < 300; attempt++) {
    if (catalogServerError || catalogServer.exitCode !== null) throw new Error('The local catalog server stopped before readiness');
    sourceReady = await fetch(`${catalogOrigin}/api/v1/ready`, { signal: AbortSignal.timeout(1000) }).then((r) => r.ok).catch(() => false);
    if (sourceReady) break;
    await pause(100);
  }
  if (!sourceReady) throw new Error('The local catalog server did not reach readiness');
  const sourceManifestResponse = await fetch(`${catalogOrigin}/api/v1/catalog/manifest`, { signal: AbortSignal.timeout(10000) });
  if (!sourceManifestResponse.ok) throw new Error('The local catalog manifest request failed');
  const sourceManifest = await sourceManifestResponse.json();
  gateway = spawn('/usr/bin/sandbox-exec', ['-p', profile, capturedBinary, 'dev', '--no-open'], {
    cwd: work, env: environment, stdio: ['ignore', 'pipe', 'pipe'],
  });
  gateway.stdout.on('data', (data) => { banner += data; });
  gateway.stderr.on('data', (data) => { banner += data; });
  gatewayExit = new Promise((resolve, reject) => {
    gateway.once('exit', resolve);
    gateway.once('error', reject);
  });
  let launch;
  let ready = false;
  for (let attempt = 0; attempt < 300; attempt++) {
    if (gateway.exitCode !== null) throw new Error('The gateway stopped before readiness');
    launch = /^Console \(one-time launch link\): (\S+)/m.exec(banner)?.[1];
    ready = await fetch(`${origin}/health/ready`, { signal: AbortSignal.timeout(1000) }).then((r) => r.ok).catch(() => false);
    if (launch && ready) break;
    await pause(100);
  }
  if (!launch || !ready) throw new Error('The gateway did not issue a Console launch link and reach readiness');
  const key = /^Gateway API key \(shown once\): (\S+)/m.exec(banner)?.[1];
  if (!key) throw new Error('The gateway did not issue an API key');
  const catalogRequest = async (route, method = 'GET') => {
    const response = await fetch(origin + route, { method,
      headers: { Authorization: `Bearer ${key}` }, signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error(`Catalog request failed: ${response.status} ${route}`);
    return response.json();
  };
  const startupStatus = await catalogRequest('/api/v1/admin/catalog/status');
  console.log(JSON.stringify({ stage: 'startup-catalog', runtime: startupStatus.runtime, route_validation: startupStatus.route_validation, source_health: startupStatus.source_health, freshness: startupStatus.freshness }));
  let catalog;
  const adoptionDeadline = Date.now() + 120000;
  while (Date.now() < adoptionDeadline) {
    const response = await fetch(`${origin}/api/v1/catalog`, {
      headers: { Authorization: `Bearer ${key}` }, signal: AbortSignal.timeout(10000) });
    if (response.ok) { catalog = await response.json(); break; }
    if (![503, 429].includes(response.status)) throw new Error(`Catalog adoption request failed: ${response.status}`);
    const retrySeconds = Number(response.headers.get('retry-after'));
    await pause(Math.min(Math.max(0, adoptionDeadline - Date.now()), Number.isFinite(retrySeconds) && retrySeconds > 0 ? retrySeconds * 1000 : 1000));
  }
  if (!catalog) throw new Error('The gateway did not serve its required Starmap catalog within two minutes');
  const catalogStatus = await catalogRequest('/api/v1/admin/catalog/status');
  const freshness = catalogStatus.freshness;
  const accepted = catalogStatus.route_validation?.accepted;
  if (catalogStatus.route_validation?.state !== 'accepted' || accepted?.generation_id !== catalog.generation_id ||
      accepted?.payload_checksum !== sourceManifest.payload.checksum || catalogStatus.snapshot?.payload_checksum !== sourceManifest.payload.checksum) {
    throw new Error('The gateway must accept the exact payload served by the selected Starmap source');
  }
  if (catalog.fallback || !catalog.usable || catalog.freshness !== 'current' || catalog.age_seconds < 0 || catalog.age_seconds > 3600) {
    throw new Error(`The capture requires a current catalog less than one hour old without fallback: freshness=${catalog.freshness}, age_seconds=${catalog.age_seconds}, fallback=${catalog.fallback}`);
  }
  if (catalogStatus.source_health !== 'ok' || freshness?.source_check !== 'current' || freshness.source_check_age_seconds < 0 || freshness.source_check_age_seconds > 120) {
    throw new Error('The capture requires a successful Starmap source check within two minutes');
  }
  if (catalogStatus.snapshot?.degraded || catalogStatus.snapshot?.validation?.status !== 'passed') {
    throw new Error('The capture requires a complete validated catalog source');
  }
  const selectedModel = (await catalogRequest('/api/v1/models')).data.find((model) => model.id === 'openai/gpt-6.1-sol');
  const selectedOffering = selectedModel?.offerings?.find((offering) => offering.provider === 'openai' && offering.provider_model_id === 'gpt-6.1-sol');
  if (!selectedOffering?.operations?.includes('chat-completions')) throw new Error('The selected model has no exact OpenAI Chat Completions offering');
  browser = await chromium.launch({ executablePath: browserPath, headless: true });
  context = await browser.newContext({ viewport: { width: 1280, height: 800 },
    deviceScaleFactor: 1, colorScheme: 'dark', recordVideo: { dir: work, size: { width: 1280, height: 800 } } });
  await context.route('**/*', (route) => {
    const url = new URL(route.request().url());
    return ['127.0.0.1', 'localhost'].includes(url.hostname) ? route.continue() : route.abort();
  });
  // Browser video omits the operating system pointer. Draw its actual event position.
  await context.addInitScript(() => {
    addEventListener('DOMContentLoaded', () => {
      const pointer = document.createElement('div');
      pointer.id = 'demo-pointer';
      pointer.setAttribute('aria-hidden', 'true');
      pointer.style.cssText = 'position:fixed;left:0;top:0;width:25px;height:30px;z-index:2147483647;pointer-events:none;transform:translate(1050px,90px);filter:drop-shadow(0 2px 3px #0009)';
      pointer.innerHTML = '<svg width="25" height="30" viewBox="0 0 25 30"><path d="M2 2L2 24L8 18L13 28L17 26L12 17L21 17Z" fill="white" stroke="#14151a" stroke-width="1.5"/></svg>';
      document.body.append(pointer);
      addEventListener('mousemove', (event) => {
        pointer.style.transform = `translate(${event.clientX}px,${event.clientY}px)`;
      });
      addEventListener('mousedown', (event) => {
        const ring = document.createElement('div');
        ring.style.cssText = `position:fixed;left:${event.clientX - 14}px;top:${event.clientY - 14}px;width:28px;height:28px;border:2px solid #a78bfa;border-radius:50%;z-index:2147483646;pointer-events:none`;
        document.body.append(ring);
        const pulse = ring.animate([{ transform: 'scale(.4)', opacity: 1 }, { transform: 'scale(1.5)', opacity: 0 }], { duration: 550 });
        pulse.onfinish = () => ring.remove();
      });
    });
  });
  const page = await context.newPage();
  const credentialWrites = [];
  page.on('request', (request) => {
    const pathname = new URL(request.url()).pathname;
    if (pathname.includes('credentials') && !['GET', 'HEAD'].includes(request.method())) {
      credentialWrites.push({ method: request.method(), path: pathname });
    }
  });
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('response', (response) => { if (response.status() >= 400) errors.push(`HTTP ${response.status()} ${new URL(response.url()).pathname}`); });
  const videoStarted = performance.now();
  await page.goto(launch);
  await page.getByRole('heading', { name: 'Status: Ready', exact: true }).waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.getByTestId('catalog-age').waitFor();
  if (await page.getByTestId('catalog-fallback-pill').count() || await page.getByTestId('catalog-degraded-pill').count()) throw new Error('The Console shows catalog fallback or degradation');
  await page.locator('#demo-pointer').waitFor();
  if (await page.getByTestId('catalog-freshness-dot').getAttribute('data-verdict') !== 'fresh') throw new Error('The Console does not show a fresh catalog');
  await pause(800);
  const start = (performance.now() - videoStarted) / 1000;
  const chapters = [];
  const chapter = async (number, title, subtitle, hold = 1900) => {
    const entry = { number, title, subtitle, seconds: +((performance.now() - videoStarted) / 1000 - start).toFixed(3), hold_ms: hold };
    chapters.push(entry);
    await page.evaluate(async ({ number, title, subtitle }) => {
      document.querySelector('#demo-pointer').style.visibility = 'hidden';
      const card = document.createElement('div');
      card.id = 'demo-title';
      card.setAttribute('aria-hidden', 'true');
      card.style.cssText = 'position:fixed;inset:0;z-index:2147483645;display:grid;place-items:center;background:rgba(9,10,13,.94);pointer-events:none;opacity:0';
      const content = document.createElement('div');
      content.style.cssText = 'width:920px;max-width:calc(100% - 96px);color:#f7f7f8;font-family:Geist,sans-serif';
      const label = document.createElement('div');
      label.textContent = number === 0 ? 'STARPORT CONSOLE  /  YOUR GOAL'
        : number === null ? 'STARPORT CONSOLE  /  RESULT AND NEXT STEP'
          : `STARPORT CONSOLE  /  ${String(number).padStart(2, '0')} OF 03`;
      label.style.cssText = 'font-family:"Geist Mono",monospace;font-size:15px;letter-spacing:.14em;color:#efb52e;margin-bottom:24px';
      const heading = document.createElement('div');
      heading.textContent = title;
      heading.style.cssText = 'font-size:44px;font-weight:600;line-height:1.15;margin-bottom:18px';
      const detail = document.createElement('div');
      detail.textContent = subtitle;
      detail.style.cssText = 'font-size:22px;line-height:1.5;color:#b5b7c0';
      content.append(label, heading, detail);
      card.append(content);
      document.body.append(card);
      await card.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 180, fill: 'forwards' }).finished;
    }, { number, title, subtitle });
    if (number === 0) await page.screenshot({ path: path.join(output, 'title-preview.png') });
    await pause(hold);
    if (number === null) {
      entry.end_seconds = +((performance.now() - videoStarted) / 1000 - start).toFixed(3);
      return;
    }
    await page.evaluate(async () => {
      const card = document.querySelector('#demo-title');
      await card.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 180, fill: 'forwards' }).finished;
      card.remove();
      document.querySelector('#demo-pointer').style.visibility = 'visible';
    });
    entry.end_seconds = +((performance.now() - videoStarted) / 1000 - start).toFixed(3);
  };
  const beat = async (name, hold) => {
    scenes.push({ name, seconds: +((performance.now() - videoStarted) / 1000 - start).toFixed(3), hold_ms: hold, path: new URL(page.url()).pathname });
    await page.screenshot({ path: path.join(work, `${name}.png`) });
    if (name === 'overview') await page.screenshot({ path: path.join(output, 'poster.png') });
    await pause(hold);
  };
  const pointerActions = [];
  let pointerPosition = { x: 1050, y: 90 };
  const movePointer = async (target, name) => {
    const box = await target.boundingBox();
    if (!box) throw new Error(`Missing pointer target: ${name}`);
    const end = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
    for (let step = 1; step <= 24; step++) {
      const t = step / 24;
      const ease = t * t * (3 - 2 * t);
      await page.mouse.move(pointerPosition.x + (end.x - pointerPosition.x) * ease,
        pointerPosition.y + (end.y - pointerPosition.y) * ease);
      await pause(20);
    }
    pointerPosition = end;
    pointerActions.push({ name, seconds: +((performance.now() - videoStarted) / 1000 - start).toFixed(3), ...end });
    await pause(150);
  };
  const click = async (target, name) => {
    await movePointer(target, name);
    await page.mouse.click(pointerPosition.x, pointerPosition.y);
    await pause(200);
  };
  await page.mouse.move(pointerPosition.x, pointerPosition.y);
  await chapter(0, 'Choose a model for your app', 'Find GPT-6.1 Sol, check its offerings, and locate its provider credential setup.', 2400);
  await beat('overview', 1800);
  await chapter(1, 'Find a model', 'Search the current catalog for gpt-6.1-sol.');
  await click(page.getByRole('link', { name: 'Models', exact: true }), 'open-models');
  await page.getByLabel('Search models', { exact: true }).waitFor();
  await beat('models', 600);
  await click(page.getByLabel('Search models', { exact: true }), 'focus-model-search');
  await page.getByLabel('Search models', { exact: true }).pressSequentially('gpt-6.1-sol', { delay: 110 });
  await page.waitForFunction(() => new URL(location.href).searchParams.get('q') === 'gpt-6.1-sol');
  await beat('search', 1800);
  await chapter(2, 'Check model requirements', 'Compare capabilities, prices, and provider credential state.');
  await click(page.locator('a[href*="gpt-6.1-sol"]').first(), 'open-model-detail');
  await page.getByRole('heading', { name: /GPT-6\.1 Sol/i }).waitFor();
  const modelId = (await page.getByTitle('Copy model ID', { exact: true }).textContent()).trim();
  if (modelId !== 'openai/gpt-6.1-sol') throw new Error('The story must inspect the exact model selected for the application');
  await beat('model-detail', 3500);
  await chapter(3, 'Locate the provider credential', 'Follow the OpenAI offering to its credential setup.');
  await click(page.locator('a[href="/providers/openai"]').first(), 'open-model-provider');
  await page.getByRole('heading', { name: 'OpenAI', exact: true }).waitFor();
  const credentialCard = page.getByTestId('provider-credential-card');
  await credentialCard.getByText('Nothing pays OpenAI yet', { exact: false }).waitFor();
  await beat('provider-credential', 3200);
  await click(credentialCard.getByRole('button', { name: 'Set shared credential', exact: true }), 'open-credential-setup');
  const dialog = page.getByRole('dialog');
  await dialog.getByRole('heading', { name: 'Set shared credential', exact: true }).waitFor();
  const secrets = dialog.locator('input[type="password"]');
  if (!await secrets.count() || (await secrets.evaluateAll((inputs) => inputs.some((input) => input.value !== '')))) {
    throw new Error('The provider credential setup must show empty secret fields');
  }
  if (!await dialog.getByRole('button', { name: 'Apply credential', exact: true }).isDisabled()) {
    throw new Error('Credential apply must stay disabled without a secret');
  }
  const provider = (await catalogRequest('/api/v1/admin/providers')).providers.find((entry) => entry.provider_id === 'openai');
  if (!provider || provider.operator_credential?.usable || credentialWrites.length) {
    throw new Error('The story must finish with a missing OpenAI credential and no credential writes');
  }
  await beat('credential-setup', 2500);
  await chapter(null, 'Model inspected. Credential needed.', 'Set your shared OpenAI credential here, then use the gateway API key in your client.', 3500);
  const duration = (performance.now() - videoStarted) / 1000 - start;
  const video = page.video();
  await context.close();
  context = null;
  const raw = await video.path();
  const encode = (file, extra) => execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-ss', start.toFixed(3), '-i', raw,
    '-t', duration.toFixed(3), ...extra, path.join(output, file)], { stdio: 'pipe' });
  encode('console.mp4', ['-an', '-vf', 'tpad=stop_mode=clone:stop_duration=1', '-c:v', 'libx264', '-crf', '20', '-pix_fmt', 'yuv420p', '-movflags', '+faststart']);
  encode('console.webm', ['-an', '-vf', 'tpad=stop_mode=clone:stop_duration=1', '-c:v', 'libvpx-vp9', '-crf', '30', '-b:v', '0']);
  encode('console.gif', ['-vf', 'tpad=stop_mode=clone:stop_duration=1,fps=10,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=none', '-loop', '0']);
  const mediaDurations = Object.fromEntries(['console.mp4', 'console.webm', 'console.gif'].map((file) => {
    const elapsed = Number(execFileSync('ffprobe', ['-v', 'error', '-show_entries', 'format=duration',
      '-of', 'default=nw=1:nk=1', path.join(output, file)], { encoding: 'utf8' }).trim());
    if (!Number.isFinite(elapsed) || Math.abs(elapsed - duration) > 0.15) throw new Error(`Media duration mismatch: ${file}`);
    return [file, elapsed];
  }));
  const exit = await stopGateway();
  stopped = true;
  if (exit !== 0 || errors.length) throw new Error(`Capture failed: exit=${exit}; browser errors=${errors.join(', ')}`);
  const catalogExit = await stopCatalog();
  catalogStopped = true;
  if (catalogExit !== 0) throw new Error(`Catalog server cleanup failed: ${catalogExit}`);
  for (const [relative, expected] of Object.entries(catalogInputs)) {
    if (hash(path.join(starmapSource, relative)) !== expected) throw new Error('Catalog source inputs changed during capture');
  }
  const record = { kind: 'current-source-console', captured_at: new Date().toISOString(),
    source_commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(),
    binary_sha256: hash(capturedBinary), recorder_sha256: hash(fileURLToPath(import.meta.url)),
    transcript_sha256: hash(path.join(root, 'docs/assets/console-demo/TRANSCRIPT.md')),
    browser: browser.version(), playwright: createRequire(import.meta.url)(`${modulePath}/package.json`).version,
    viewport: { width: 1280, height: 800 }, gif_size: { width: 960, height: 600 }, duration_seconds: +duration.toFixed(3),
    media_duration_seconds: mediaDurations, final_frame_padding: 'Preserve the final scene hold when browser frame delivery ends early',
    chapters, scenes, story: { goal: 'Choose a model and locate its provider credential setup',
      model_id: modelId, provider_id: provider.provider_id, provider_model_id: selectedOffering.provider_model_id,
      result: 'Model inspected; provider credential missing; empty credential setup opened',
      provider_credential: provider.operator_credential, credential_writes: credentialWrites,
      next_step: 'Set a shared OpenAI provider credential, then connect a client with the gateway API key' },
    pointer: { kind: 'browser mouse event overlay', actions: pointerActions, click_pulse_ms: 550 }, search_typing_ms: 110, provider_requests: 0, provider_credentials_present: false, network: 'loopback only; gateway reads the local Starmap server',
    catalog_source: { kind: 'starmap', exit_code: catalogExit, binary_sha256: hash(capturedStarmap), manifest: sourceManifest,
      source_commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: starmapSource, encoding: 'utf8' }).trim(),
      reviewed_local_catalog: true, inputs: catalogInputs },
    catalog: { summary: catalog, adoption: { startup_policy: 'require_source', source_generation_id: sourceManifest.generation_id, effective_generation_id: catalog.generation_id, payload_checksum: accepted.payload_checksum }, route_validation: catalogStatus.route_validation, freshness, capture_limits: { catalog_age_seconds: 3600, source_check_age_seconds: 120 }, snapshot: catalogStatus.snapshot, source_health: catalogStatus.source_health, acquisition: catalogStatus.acquisition },
    catalog_acquisition_sources: [],
    gateway_exit_code: exit, browser_errors: errors, artifacts: Object.fromEntries(['poster.png', 'title-preview.png', 'console.mp4', 'console.webm', 'console.gif'].map((file) => {
      const artifact = path.join(output, file);
      const bytes = statSync(artifact).size;
      if (!bytes) throw new Error(`Empty capture: ${file}`);
      return [file, { sha256: hash(artifact), bytes }];
    })), verdict: 'PASS' };
  writeFileSync(path.join(output, 'record.json'), JSON.stringify(record, null, 2) + '\n');
  console.log(`PASS Console capture: ${scenes.length} scenes, ${duration.toFixed(1)} seconds`);
} finally {
  if (context) await context.close().catch(() => {});
  if (browser) await browser.close().catch(() => {});
  await Promise.allSettled([stopped ? undefined : stopGateway(), catalogStopped ? undefined : stopCatalog()]);
  rmSync(work, { recursive: true, force: true });
}
