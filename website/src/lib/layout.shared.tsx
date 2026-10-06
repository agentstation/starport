import Image from 'next/image';
import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared';

export const GITHUB_URL = 'https://github.com/agentstation/starport';

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: (
        <>
          {/* The mark is decoration beside the name, so it has empty alt text. */}
          <Image src="/favicon.svg" alt="" width={20} height={20} />
          <span className="font-semibold">Starport</span>
        </>
      ),
      url: '/',
    },
    githubUrl: GITHUB_URL,
    links: [{ text: 'Documentation', url: '/docs', active: 'nested-url' }],
  };
}
