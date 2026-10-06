import './splash.css';

// The splash page has its own night surface and its own type. Every rule in
// splash.css is scoped under `.splash`, so nothing leaks into the docs.
export default function Layout({ children }: LayoutProps<'/'>) {
  return <div className="splash">{children}</div>;
}
