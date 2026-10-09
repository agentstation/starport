import { Fragment } from 'react';

// Inline renders splash prose. A span between backticks is a machine value,
// so it renders as code in mono.
export function Inline({ text }: { text: string }) {
  return text.split('`').map((part, index) =>
    index % 2 === 1 ? <code key={index}>{part}</code> : <Fragment key={index}>{part}</Fragment>,
  );
}
