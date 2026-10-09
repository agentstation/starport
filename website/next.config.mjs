import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createMDX } from 'fumadocs-mdx/next';

const withMDX = createMDX();

// The content tree and the console area list sit outside this package, so
// the bundler root is the repository root.
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

/** @type {import('next').NextConfig} */
const config = {
  output: 'export',
  trailingSlash: false,
  images: { unoptimized: true },
  reactStrictMode: true,
  poweredByHeader: false,
  turbopack: { root: repoRoot },
  outputFileTracingRoot: repoRoot,
};

export default withMDX(config);
