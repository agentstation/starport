import { readFileSync } from 'node:fs';
import path from 'node:path';

// The splash page reads its facts from the repository at build time. Thus
// the page and the README cannot drift. test/splash.test.ts checks that every
// command below is a verbatim part of README.md.

const repoRoot = path.resolve(process.cwd(), '..');

// leadSentence returns the start of docs/site/index.md up to the first full
// stop.
export function leadSentence(): string {
  const text = readFileSync(path.join(repoRoot, 'docs/site/index.md'), 'utf8').replace(/\r\n/g, '\n');
  const body = text.startsWith('---\n') ? text.slice(text.indexOf('\n---\n', 3) + 5) : text;
  const paragraph = body.trim().split('\n\n')[0] ?? '';
  const end = paragraph.indexOf('. ');
  return end < 0 ? paragraph : paragraph.slice(0, end + 1);
}

// posterSize reads the width and the height from the PNG header of the demo
// poster. The page uses them to keep the space of the demo before it loads.
export function posterSize(): { width: number; height: number } {
  const png = readFileSync(path.join(repoRoot, 'docs/assets/first-use-v1.2.0/poster.png'));
  return { width: png.readUInt32BE(16), height: png.readUInt32BE(20) };
}

export type InstallMethod = { title: string; note: string; command: string };

export const INSTALL_METHODS: InstallMethod[] = [
  {
    title: 'Homebrew',
    note: 'Install the released cask on macOS or Linux.',
    command: `brew trust --cask agentstation/tap/starport
brew install --cask agentstation/tap/starport
starport --version`,
  },
  {
    title: 'Container image',
    note: 'Pull a versioned image and verify its GitHub attestation.',
    command: `STARPORT_VERSION="$(gh release view \\
  --repo agentstation/starport \\
  --json tagName \\
  --jq '.tagName | ltrimstr("v")')"
docker pull "ghcr.io/agentstation/starport:$STARPORT_VERSION"
gh attestation verify "oci://ghcr.io/agentstation/starport:$STARPORT_VERSION" \\
  --repo agentstation/starport \\
  --signer-workflow agentstation/starport/.github/workflows/release.yaml
docker run --rm "ghcr.io/agentstation/starport:$STARPORT_VERSION" --version`,
  },
  {
    title: 'Quick start',
    note: 'Start a temporary gateway with one provider inference credential. It keeps no state after it stops.',
    command: `unset STARPORT_CATALOG_STATE_DIR STARPORT_FILES_BACKEND
export OPENAI_API_KEY="replace-with-provider-inference-key"
starport dev`,
  },
];
