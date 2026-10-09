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
