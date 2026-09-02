import { useState } from "react";

import { Card, CardTitle } from "@/components/ui/Card";
import { CopyButton } from "@/components/ui/CopyButton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

function snippets(origin: string): Record<string, string> {
  return {
    curl: `curl ${origin}/v1/chat/completions \\
  -H "Authorization: Bearer $STARPORT_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "anthropic/claude-sonnet-5", "messages": [{"role": "user", "content": "Hello"}]}'`,
    python: `from openai import OpenAI

client = OpenAI(base_url="${origin}/v1", api_key=STARPORT_API_KEY)
reply = client.chat.completions.create(
    model="anthropic/claude-sonnet-5",
    messages=[{"role": "user", "content": "Hello"}],
)`,
    javascript: `import OpenAI from "openai";

const client = new OpenAI({ baseURL: "${origin}/v1", apiKey: STARPORT_API_KEY });
const reply = await client.chat.completions.create({
  model: "anthropic/claude-sonnet-5",
  messages: [{ role: "user", content: "Hello" }],
});`,
  };
}

// QuickstartCard shows drop-in client snippets with language tabs.
export function QuickstartCard() {
  const all = snippets(location.origin);
  const [current, setCurrent] = useState("curl");
  return (
    <Card>
      <CardTitle aside={<span className="text-xs">drop-in OpenAI client</span>}>
        Quickstart
      </CardTitle>
      <Tabs value={current} onValueChange={(value) => setCurrent(String(value))}>
        <TabsList aria-label="Snippet language" className="gap-0">
          {Object.keys(all).map((name) => (
            <TabsTrigger key={name} value={name} className="h-8 px-2.5 text-xs">
              {name}
            </TabsTrigger>
          ))}
        </TabsList>
        {Object.entries(all).map(([name, snippet]) => (
          <TabsContent key={name} value={name}>
            <div className="relative rounded-sm border border-border-1 bg-bg-canvas">
              <div className="absolute right-1.5 top-1.5">
                <CopyButton text={snippet} label="snippet" />
              </div>
              <pre className="overflow-x-auto p-3 pr-10 font-mono text-xs leading-4 text-text-2">
                <code>{snippet}</code>
              </pre>
            </div>
          </TabsContent>
        ))}
      </Tabs>
    </Card>
  );
}
