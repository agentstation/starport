import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { INSTALL_METHODS, leadSentence, posterSize } from '../src/lib/splash';

const readme = readFileSync(path.resolve(process.cwd(), '..', 'README.md'), 'utf8').replace(/\r\n/g, '\n');

describe('splash facts', () => {
  it('takes one sentence from the documentation home page', () => {
    const sentence = leadSentence();
    expect(sentence).toMatch(/^Starport is .+\.$/);
    expect(sentence.slice(0, -1)).not.toContain('. ');
  });

  it.each(INSTALL_METHODS.map((method) => [method.title, method.command]))(
    'copies the %s commands verbatim from README.md',
    (_title, command) => {
      expect(readme).toContain(command);
    },
  );

  it('reads the poster size from the PNG header', () => {
    expect(posterSize()).toEqual({ width: 1280, height: 800 });
  });
});
