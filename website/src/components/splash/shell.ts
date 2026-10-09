// A line of a code block, with the marks that the page draws before it. A
// shell command gets a `$` prompt. A line that continues the command above it
// (after a trailing backslash) and a comment line get no prompt.
export type CodeLine = { text: string; prompt: boolean; comment: boolean };

export function codeLines(text: string, shell: boolean): CodeLine[] {
  let continued = false;
  return text.split('\n').map((line) => {
    const comment = shell && line.startsWith('#');
    const prompt = shell && !comment && !continued && line.trim() !== '';
    continued = shell && line.endsWith('\\');
    return { text: line, prompt, comment };
  });
}
