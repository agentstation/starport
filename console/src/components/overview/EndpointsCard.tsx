import { Card, CardTitle } from "@/components/ui/Card";
import { CopyButton } from "@/components/ui/CopyButton";

// EndpointsCard lists the three gateway URLs a client needs. The label of
// each row is its copy control: one element names the endpoint and copies
// it, so the row never says the same name twice.
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
            <CopyButton
              text={endpoint.url}
              label={endpoint.label}
              className="-ml-1.5 w-32 justify-start"
            />
            <code className="min-w-0 flex-1 truncate font-mono text-sm text-text-2">
              {endpoint.url}
            </code>
          </div>
        ))}
      </div>
    </Card>
  );
}
