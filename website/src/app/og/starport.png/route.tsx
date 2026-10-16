import { ImageResponse } from 'next/og';

import { SOCIAL_CARD } from '@/lib/social-card';
import { HEADLINE, readRepoFile } from '@/lib/splash';

// The build writes the social card to out/og/starport.png. It uses the night
// ground and the gold of the splash page, the favicon star, and the splash
// headline. The default face of next/og is Geist, the site face.
export const dynamic = 'force-static';

export function GET() {
  const mark = `data:image/svg+xml;base64,${Buffer.from(readRepoFile('console/src/favicon.svg')).toString('base64')}`;
  return new ImageResponse(
    (
      <div
        style={{
          width: '100%',
          height: '100%',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: '72px 96px',
          background: '#0a0b0c',
          color: '#f6f7f8',
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', height: '100%', justifyContent: 'space-between' }}>
          <div style={{ display: 'flex', fontSize: 44, color: '#f0b23e' }}>Starport</div>
          <div style={{ display: 'flex', flexDirection: 'column', fontSize: 64, lineHeight: 1.12, letterSpacing: '-0.02em' }}>
            {HEADLINE.map((line) => (
              <div key={line} style={{ display: 'flex' }}>
                {line}
              </div>
            ))}
          </div>
          <div style={{ display: 'flex', fontSize: 28, color: '#8b909a' }}>starport.agentstation.ai</div>
        </div>
        {/* The star is decoration beside the name, so it has empty alt text.
            next/og draws the img element into the PNG; next/image does not apply. */}
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src={mark} width={312} height={300} alt="" />
      </div>
    ),
    { width: SOCIAL_CARD.width, height: SOCIAL_CARD.height },
  );
}
