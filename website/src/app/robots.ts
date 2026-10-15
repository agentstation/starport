import type { MetadataRoute } from 'next';

import { SITE_URL } from '@/lib/layout.shared';

// The build writes out/robots.txt. Cloudflare puts its managed content
// signals before this file in the served response.
export const dynamic = 'force-static';

export default function robots(): MetadataRoute.Robots {
  return {
    rules: { userAgent: '*', allow: '/' },
    sitemap: new URL('/sitemap.xml', SITE_URL).href,
  };
}
