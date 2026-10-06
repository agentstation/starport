import Link from 'next/link';
import { HomeLayout } from 'fumadocs-ui/layouts/home';

import { baseOptions } from '@/lib/layout.shared';

export default function NotFound() {
  return (
    <HomeLayout {...baseOptions()}>
      <main className="mx-auto w-full max-w-4xl px-4 py-16">
        <h1 className="text-3xl font-semibold">Page not found</h1>
        <p className="mt-4 text-fd-muted-foreground">This address has no page.</p>
        <p className="mt-6 flex gap-4">
          <Link href="/" className="underline">
            Home
          </Link>
          <Link href="/docs" className="underline">
            Documentation
          </Link>
        </p>
      </main>
    </HomeLayout>
  );
}
