import type { Metadata } from 'next';
import { RootProvider } from 'fumadocs-ui/provider/next';

import { SITE_URL } from '@/lib/layout.shared';
import { SOCIAL_CARD } from '@/lib/social-card';
import './global.css';

export const metadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  title: {
    default: 'Starport',
    template: '%s · Starport',
  },
  description:
    'Starport is a self-hosted LLM inference gateway in one binary, with OpenAI-compatible and OpenRouter-compatible APIs.',
  icons: { icon: { url: '/favicon.svg', type: 'image/svg+xml' } },
  // The social cards leave out the title and the description, so each page
  // gives its own. The URL './' resolves to the path of each page. The
  // Twitter card copies the Open Graph image.
  openGraph: { type: 'website', siteName: 'Starport', url: './', images: [SOCIAL_CARD] },
  twitter: { card: 'summary_large_image' },
};

export default function Layout({ children }: LayoutProps<'/'>) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body className="flex min-h-screen flex-col">
        <RootProvider
          // The build writes the search index as a static file, and the
          // browser searches it. The site has no search server.
          search={{ options: { type: 'static', api: '/docs/search.json' } }}
        >
          {children}
        </RootProvider>
      </body>
    </html>
  );
}
