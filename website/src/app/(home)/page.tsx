import Image from 'next/image';
import Link from 'next/link';

import { GITHUB_URL } from '@/lib/layout.shared';
import { INSTALL_METHODS, leadSentence, posterSize } from '@/lib/splash';

const DOCS_LINKS = [
  { href: '/docs/start', title: 'Start', text: 'Run a first gateway and send a first request.' },
  { href: '/docs/operate-starport', title: 'Operate Starport', text: 'Run, observe, and upgrade a gateway.' },
  { href: '/docs/api-compatibility', title: 'API compatibility', text: 'See the OpenAI and OpenRouter surfaces.' },
];

export default function HomePage() {
  const poster = posterSize();
  return (
    <main className="mx-auto w-full max-w-4xl px-4 py-12 md:py-16">
      <h1 className="text-3xl font-semibold tracking-tight md:text-4xl">Starport</h1>
      <p className="mt-4 text-lg text-fd-muted-foreground">{leadSentence()}</p>
      <p className="mt-6 flex flex-wrap gap-3">
        <Link
          href="/docs/start"
          className="rounded-md bg-fd-primary px-4 py-2 font-medium text-fd-primary-foreground"
        >
          Start
        </Link>
        <a href={GITHUB_URL} className="rounded-md border px-4 py-2 font-medium">
          GitHub
        </a>
      </p>

      <figure className="mt-10">
        {/* The preview is static, as in the README. The link opens the
            38-second recording, so the page plays no animation on its own. */}
        <a href="/demo/first-use.gif" className="block overflow-hidden rounded-lg border">
          <Image
            src="/demo/poster.png"
            width={poster.width}
            height={poster.height}
            alt="A terminal installs Starport, reads the catalog, starts a temporary gateway, and streams an OpenAI answer. Select the preview to play the recording."
            className="h-auto w-full"
            priority
          />
        </a>
        <figcaption className="mt-2 text-sm text-fd-muted-foreground">
          <a href="/demo/first-use.gif" className="underline">
            Watch the 38-second first request
          </a>{' '}
          or read the{' '}
          <a href={`${GITHUB_URL}/blob/main/docs/assets/first-use-v1.2.0/TRANSCRIPT.md`} className="underline">
            transcript
          </a>
          .
        </figcaption>
      </figure>

      <section aria-labelledby="install" className="mt-12">
        <h2 id="install" className="text-2xl font-semibold">
          Install
        </h2>
        <p className="mt-2 text-fd-muted-foreground">
          Supported targets are macOS on Apple silicon, Linux on x86-64 and ARM64, and Windows on x86-64 and ARM64.
          Checksummed archives for macOS, Linux, and Windows are on{' '}
          <a href={`${GITHUB_URL}/releases`} className="underline">
            GitHub Releases
          </a>
          .
        </p>
        {INSTALL_METHODS.map((method) => (
          <div key={method.title} className="mt-6">
            <h3 className="font-semibold">{method.title}</h3>
            <p className="mt-1 text-sm text-fd-muted-foreground">{method.note}</p>
            <pre className="mt-2 overflow-x-auto rounded-lg border bg-fd-muted p-4 text-sm">
              <code>{method.command}</code>
            </pre>
          </div>
        ))}
      </section>

      <section aria-labelledby="read" className="mt-12">
        <h2 id="read" className="text-2xl font-semibold">
          Read the documentation
        </h2>
        <ul className="mt-4 grid gap-4 md:grid-cols-3">
          {DOCS_LINKS.map((link) => (
            <li key={link.href}>
              <Link href={link.href} className="block h-full rounded-lg border p-4 hover:bg-fd-accent">
                <span className="font-semibold text-fd-primary">{link.title}</span>
                <span className="mt-1 block text-sm text-fd-muted-foreground">{link.text}</span>
              </Link>
            </li>
          ))}
        </ul>
      </section>
    </main>
  );
}
