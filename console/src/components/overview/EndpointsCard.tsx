import { Card, CardTitle } from "@/components/ui/Card";
import { CopyButton } from "@/components/ui/CopyButton";

// EndpointsCard lists the three gateway URLs a client needs, each with a
// copy chip.
export function EndpointsCard() {
  const origin = location.origin;
  const endpoints = [
    { label: "OpenAI SDK", url: `${origin}/v1` },
    { label: "OpenRouter SDK", url: `${origin}/api/v1` },
    { label: "Health", url: `${origin}/health/ready` },
  ];
  return (
    <Card>
      <CardTitle>Endpoints</CardTitle>
      <div className="flex flex-col gap-2">
        {endpoints.map((endpoint) => (
          <div key={endpoint.url} className="flex items-center gap-3">
            <span className="w-28 shrink-0 text-xs text-text-3">{endpoint.label}</span>
            <code className="min-w-0 flex-1 truncate font-mono text-sm text-text-2">
              {endpoint.url}
            </code>
            <CopyButton text={endpoint.url} label={endpoint.label} />
          </div>
        ))}
      </div>
    </Card>
  );
}
