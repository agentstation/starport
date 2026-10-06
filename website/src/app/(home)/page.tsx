import Image from 'next/image';
import Link from 'next/link';
import { preload } from 'react-dom';

import { HeroScene, HeroScroll } from '@/components/journey/hero-scene';
import { Journey, SkipToEnd } from '@/components/journey/journey';
import { Inline } from '@/components/splash/inline';
import { InstallTabs } from '@/components/splash/install-tabs';
import { GITHUB_URL } from '@/lib/layout.shared';
import { HEADLINE, INSTALL_METHODS, lede, posterSize, siteBuild } from '@/lib/splash';

export default function HomePage() {
  // The first paint uses the regular sans and mono faces, so the browser
  // fetches them with the document.
  preload('/fonts/Geist-Regular.woff2', { as: 'font', type: 'font/woff2', crossOrigin: 'anonymous' });
  preload('/fonts/GeistMono-Regular.woff2', { as: 'font', type: 'font/woff2', crossOrigin: 'anonymous' });

  return (
    <>
      <a className="skip" href="#content">
        Skip to the content
      </a>
      <SkipToEnd />
      <header className="nav">
        <Link href="/" className="brand">
          {/* The mark is decoration beside the name, so it has empty alt text. */}
          <Image src="/favicon.svg" alt="" width={26} height={26} />
          <span>Starport</span>
        </Link>
        <nav aria-label="Site">
          <Link href="/docs">Docs</Link>
          <a href={GITHUB_URL} className="nav-github">
            GitHub
          </a>
          <Link href="/docs/start" className="run">
            Run locally
          </Link>
        </nav>
      </header>

      <main id="content" tabIndex={-1}>
        <section className="hero" aria-labelledby="headline">
          <div className="hero-body">
            <div className="hero-copy">
              <h1 id="headline">
                {HEADLINE.map((line) => (
                  <span key={line}>{line}</span>
                ))}
              </h1>
              <p className="lede">
                <Inline text={lede()} />
              </p>
              <div className="actions">
                <Link href="/docs/start" className="pill primary">
                  Get started
                </Link>
                <Link href="/docs" className="pill">
                  Read the docs
                </Link>
              </div>
              <InstallTabs methods={INSTALL_METHODS} />
            </div>
            <HeroScene />
          </div>
          <HeroScroll />
        </section>

        <Journey poster={posterSize()} build={siteBuild()} />
      </main>
    </>
  );
}
