let OpenRouter;
try {
  ({ OpenRouter } = await import("@openrouter/sdk"));
} catch (error) {
  if (error?.code === "ERR_MODULE_NOT_FOUND") {
    console.log("UNVERIFIED TypeScript OpenRouter SDK: package '@openrouter/sdk' is not installed");
    process.exit(3);
  }
  throw error;
}

const client = new OpenRouter({
  apiKey: process.env.STARPORT_SMOKE_API_KEY,
  serverURL: process.env.STARPORT_SMOKE_BASE_URL,
});
const response = await client.chat.send({
  model: "openai/gpt-4.1",
  messages: [{ role: "user", content: "smoke" }],
  stream: false,
});
if (response.choices[0].message.content !== "starport smoke ok") {
  throw new Error("unexpected OpenRouter TypeScript SDK response");
}
console.log("PASS TypeScript OpenRouter SDK");
