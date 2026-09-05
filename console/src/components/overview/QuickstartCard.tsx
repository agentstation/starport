import { code as highlighter } from "@streamdown/code";
import type { HighlightResult } from "@streamdown/code";
import { type CSSProperties, useEffect, useState } from "react";

import { Card, CardTitle } from "@/components/ui/Card";
import { CopyButton } from "@/components/ui/CopyButton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

// A snippet names its tab and the Shiki grammar that colors it.
type Snippet = { name: string; language: "bash" | "python" | "javascript"; text: string };

function snippets(origin: string): Snippet[] {
  return [
    {
      name: "curl",
      language: "bash",
      text: `curl ${origin}/v1/chat/completions \\
  -H "Authorization: Bearer $STARPORT_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "anthropic/claude-sonnet-5", "messages": [{"role": "user", "content": "Hello"}]}'`,
    },
    {
      name: "python",
      language: "python",
      text: `from openai import OpenAI

client = OpenAI(base_url="${origin}/v1", api_key=STARPORT_API_KEY)
reply = client.chat.completions.create(
    model="anthropic/claude-sonnet-5",
    messages=[{"role": "user", "content": "Hello"}],
)`,
    },
    {
      name: "javascript",
      language: "javascript",
      text: `import OpenAI from "openai";

const client = new OpenAI({ baseURL: "${origin}/v1", apiKey: STARPORT_API_KEY });
const reply = await client.chat.completions.create({
  model: "anthropic/claude-sonnet-5",
  messages: [{ role: "user", content: "Hello" }],
});`,
    },
  ];
}

// The chat transcript colors its code blocks through Streamdown's Shiki
// plugin, and the quickstart uses the same plugin and the same two themes,
// so a snippet reads the same here as in a reply. The plugin loads a grammar
// once and answers later calls synchronously; until then the snippet renders
// as plain text, so nothing waits on the highlighter.
const THEMES = highlighter.getThemes();

function useHighlight(snippet: Snippet): HighlightResult | null {
  const [result, setResult] = useState<HighlightResult | null>(null);
  useEffect(() => {
    let current = true;
    const ready = highlighter.highlight(
      { code: snippet.text, language: snippet.language, themes: THEMES },
      (late) => {
        if (current) setResult(late);
      },
    );
    setResult(ready);
    return () => {
      current = false;
    };
  }, [snippet]);
  return result;
}

// Highlighted renders the token lines. Each token carries its light color
// and its dark color; the dark: variant follows the console theme attribute,
// the same way Streamdown's code block does.
function Highlighted({ snippet }: { snippet: Snippet }) {
  const result = useHighlight(snippet);
  if (result === null) return <>{snippet.text}</>;
  return (
    <>
      {result.tokens.map((line, index) => (
        <span key={index} data-testid="snippet-line" className="block min-h-4">
          {line.map((token, position) => {
            const style = token.htmlStyle as Record<string, string> | undefined;
            const light = style?.color;
            const dark = style?.["--shiki-dark"];
            return (
              <span
                key={position}
                className="text-[var(--sdm-c,inherit)] dark:text-[var(--shiki-dark,var(--sdm-c,inherit))]"
                style={{ "--sdm-c": light, "--shiki-dark": dark } as CSSProperties}
              >
                {token.content}
              </span>
            );
          })}
        </span>
      ))}
    </>
  );
}

// QuickstartCard shows drop-in client snippets with language tabs. The
// card, the tab panel, and the snippet frame all carry min-w-0: a grid
// item keeps its content's minimum width by default, so without it the
// widest snippet line pushed the card past its column and the shell
// clipped the line instead of the snippet scrolling.
export function QuickstartCard() {
  const [all] = useState(() => snippets(location.origin));
  const [current, setCurrent] = useState("curl");
  return (
    <Card className="min-w-0">
      <CardTitle aside={<span className="text-xs">drop-in OpenAI client</span>}>
        Quickstart
      </CardTitle>
      <Tabs value={current} onValueChange={(value) => setCurrent(String(value))}>
        {/* The copy control shares the tab row, so it never sits over the
            code when the card is narrow. */}
        <div className="flex items-center justify-between gap-2">
          <TabsList aria-label="Snippet language" className="gap-0">
            {all.map((snippet) => (
              <TabsTrigger key={snippet.name} value={snippet.name} className="h-8 px-2.5 text-xs">
                {snippet.name}
              </TabsTrigger>
            ))}
          </TabsList>
          <CopyButton
            text={() => all.find((snippet) => snippet.name === current)?.text ?? ""}
            label="snippet"
          />
        </div>
        {all.map((snippet) => (
          <TabsContent key={snippet.name} value={snippet.name} className="min-w-0">
            <div className="min-w-0 rounded-sm border border-border-1 bg-bg-canvas">
              <pre className="overflow-x-auto p-3 font-mono text-xs leading-4 text-text-2">
                <code data-testid={`snippet-${snippet.name}`}>
                  <Highlighted snippet={snippet} />
                </code>
              </pre>
            </div>
          </TabsContent>
        ))}
      </Tabs>
    </Card>
  );
}
