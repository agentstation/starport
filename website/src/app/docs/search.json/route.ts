import { createFromSource } from 'fumadocs-core/search/server';

import { source } from '@/lib/source';

// The build writes the search index to out/docs/search.json. The browser
// loads that file and searches it, so the site has no search server.
export const revalidate = false;
export const { staticGET: GET } = createFromSource(source, { language: 'english' });
