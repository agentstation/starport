import Image from 'next/image';
import Link from 'next/link';
import { preload } from 'react-dom';

import { Code, Table } from '@/components/splash/code-block';
import { Inline } from '@/components/splash/inline';
import { InstallTabs } from '@/components/splash/install-tabs';
import { RequestScene } from '@/components/splash/request-scene';
import { GITHUB_URL } from '@/lib/layout.shared';
import {
  CHAPTERS,
  type Chapter,
  HEADLINE,
  INSTALL_METHODS,
  lede,
  posterSize,
  SCENE_PARTS,
  siteBuild,
} from '@/lib/splash';

function Actions() {
  return (
    <div className="actions">
      <Link href="/docs/start" className="pill primary">
        Get started
      </Link>
      <Link href="/docs" className="pill">
        Read the docs
      </Link>
    </div>
  );
}

// Demo is the README recording. The preview is static, as in the README. The
// link opens the 38-second recording, so the page plays no animation on its
// own.
function Demo() {
  const poster = posterSize();
  return (
    <figure className="demo">
      <a href="/demo/first-use.gif">
        <Image
          src="/demo/poster.png"
          width={poster.width}
          height={poster.height}
          alt="A terminal installs Starport, reads the catalog, starts a temporary gateway, and streams an OpenAI answer. Select the preview to play the recording."
        />
      </a>
      <figcaption>
        <a href="/demo/first-use.gif">Watch the 38-second first request</a> or read the{' '}
        <a href={`${GITHUB_URL}/blob/main/docs/assets/first-use-v1.2.0/TRANSCRIPT.md`}>transcript</a>.
      </figcaption>
    </figure>
  );
}

function ChapterSection({ chapter, index }: { chapter: Chapter; index: number }) {
  const heading = `chapter-${chapter.id}`;
  return (
    <section className="chapter" aria-labelledby={heading}>
      <div className="chapter-copy">
        <p className="eyebrow">
          <span className="eyebrow-index">{String(index + 1).padStart(2, '0')}</span>
          {chapter.eyebrow}
          {chapter.status ? <em className="badge">{chapter.status.badge}</em> : null}
        </p>
        <h2 id={heading}>{chapter.claim}</h2>
        <p className="chapter-body">
          <Inline text={chapter.body} />
        </p>
        <ul className="chips" aria-label="Facts">
          {chapter.chips.map((chip) => (
            <li key={chip}>
              <Inline text={chip} />
            </li>
          ))}
        </ul>
        {chapter.status ? (
          <p className="status-note">
            <span>{chapter.status.target}</span> {chapter.status.text}
          </p>
        ) : null}
      </div>
      <div className="chapter-visual">
        {chapter.visuals.map((visual, key) =>
          visual.kind === 'code' ? <Code key={key} block={visual} /> : <Table key={key} block={visual} />,
        )}
        {chapter.demo ? <Demo /> : null}
      </div>
    </section>
  );
}

export default function HomePage() {
  // The first paint uses the regular sans and mono faces, so the browser
  // fetches them with the document.
  preload('/fonts/Geist-Regular.woff2', { as: 'font', type: 'font/woff2', crossOrigin: 'anonymous' });
  preload('/fonts/GeistMono-Regular.woff2', { as: 'font', type: 'font/woff2', crossOrigin: 'anonymous' });
  const build = siteBuild();

  return (
    <>
      <a className="skip" href="#content">
        Skip to the content
      </a>
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
          <div className="hero-copy">
            <h1 id="headline">
              {HEADLINE.map((line) => (
                <span key={line}>{line}</span>
              ))}
            </h1>
            <p className="lede">
              <Inline text={lede()} />
            </p>
            <Actions />
            <InstallTabs methods={INSTALL_METHODS} />
          </div>
          <RequestScene parts={SCENE_PARTS} />
        </section>

        <div className="chapters">
          {CHAPTERS.map((chapter, index) => (
            <ChapterSection key={chapter.id} chapter={chapter} index={index} />
          ))}
        </div>

        <section className="closing" aria-labelledby="closing">
          <h2 id="closing">Start local with one command.</h2>
          <InstallTabs methods={INSTALL_METHODS} />
          <Actions />
          <p className="build">
            This build: release <code>{build.release}</code> · Starmap <code>{build.starmap}</code>
          </p>
        </section>
      </main>

      <footer className="footer">
        <nav aria-label="Footer">
          <Link href="/docs">Docs</Link>
          <a href={GITHUB_URL}>GitHub</a>
          <a href={`${GITHUB_URL}/releases`}>Releases</a>
        </nav>
        <p>
          <span>AGPLv3</span>
          <span>Starport · one binary inference gateway</span>
        </p>
      </footer>
    </>
  );
}
