import type { MetadataRoute } from 'next';

import { SITE_URL } from '@/lib/layout.shared';
import { source } from '@/lib/source';

// The build writes out/sitemap.xml. It lists the splash page and each
// documentation page that the docs route exports, from the same source.
export const dynamic = 'force-static';

export default function sitemap(): MetadataRoute.Sitemap {
  return ['/', ...source.getPages().map((page) => page.url)].map((url) => ({ url: new URL(url, SITE_URL).href }));
}
