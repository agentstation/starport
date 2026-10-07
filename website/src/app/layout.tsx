import type { Metadata } from 'next';
import { RootProvider } from 'fumadocs-ui/provider/next';
import './global.css';

export const metadata: Metadata = {
  metadataBase: new URL('https://starport.agentstation.ai'),
  title: {
    default: 'Starport',
    template: '%s · Starport',
  },
  description:
    'Starport is a self-hosted LLM inference gateway in one binary, with OpenAI-compatible and OpenRouter-compatible APIs.',
  icons: { icon: { url: '/favicon.svg', type: 'image/svg+xml' } },
};

// The Cloudflare Web Analytics site token. It is a public identifier, not a
// secret. The build writes the beacon tag into each page, so the manifest
// lists the served bytes. The dashboard setting for the site must stay on
// manual setup. The automatic setup injects a second tag at the edge, and the
// served bytes then differ from the manifest.
const analyticsToken = 'eafc887402d04f999b2b4233a3f21f54';

export default function Layout({ children }: LayoutProps<'/'>) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        <script
          defer
          src="https://static.cloudflareinsights.com/beacon.min.js"
          data-cf-beacon={JSON.stringify({ token: analyticsToken })}
        />
      </head>
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
