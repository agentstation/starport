import type { CodeBlock, TableBlock } from '@/lib/splash';

import { codeLines } from './shell';

// The head of a block: what the block holds and the repository file that it
// copies.
function BlockHead({ label, source }: { label: string; source: string }) {
  return (
    <div className="block-head">
      <span>{label}</span>
      <span className="block-source">{source}</span>
    </div>
  );
}

export function Code({ block }: { block: CodeBlock }) {
  const shell = block.lang === 'shell';
  const label = block.title ? `${block.lang} · ${block.title}` : block.lang;
  return (
    <div className="block">
      <BlockHead label={label} source={block.source} />
      {/* A long line scrolls inside the block, so the block takes focus. */}
      <pre tabIndex={0} aria-label={`${label} from ${block.source}`}>
        <code>
          {codeLines(block.text, shell).map((line, index) => (
            <span className={line.comment ? 'line comment' : 'line'} key={index}>
              {shell ? <i aria-hidden="true">{line.prompt ? '$ ' : '  '}</i> : null}
              {line.text || ' '}
            </span>
          ))}
        </code>
      </pre>
    </div>
  );
}

export function Table({ block }: { block: TableBlock }) {
  return (
    <div className="block">
      <BlockHead label="table" source={block.source} />
      <div className="block-table" tabIndex={0} role="region" aria-label={`Table from ${block.source}`}>
        <table>
          <thead>
            <tr>
              {block.head.map((cell) => (
                <th key={cell} scope="col">
                  {cell}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {block.rows.map((row) => (
              <tr key={row[0]}>
                {row.map((cell, index) =>
                  index === 0 ? (
                    <th key={index} scope="row">
                      {cell}
                    </th>
                  ) : (
                    <td key={index}>{block.mono.includes(index) ? <code>{cell}</code> : cell}</td>
                  ),
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
