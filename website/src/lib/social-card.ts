import { HEADLINE } from './splash-facts';

// SOCIAL_CARD is the link preview image of every page. The route
// app/og/starport.png draws it, and the root layout metadata links it as
// og:image. The Twitter card copies the Open Graph image. The file name ends
// in .png, so the static asset server sends it as image/png.
export const SOCIAL_CARD = {
  url: '/og/starport.png',
  width: 1200,
  height: 630,
  type: 'image/png',
  alt: `Starport. ${HEADLINE.join(' ')}`,
};
