import { OpenRouter } from "@openrouter/sdk";

const client = new OpenRouter({
  apiKey: process.env.STARPORT_CATALOG_KEY,
  serverURL: process.env.STARPORT_CATALOG_URL + "/api/v1",
});
for (const model of ["author/current", "author/old"]) {
  for (const stream of [false, true]) {
    const removed = process.argv[2] === "removed" ||
      (process.argv[2] === "alias-removed" && model === "author/old");
    try {
      const response = await client.chat.send({ chatRequest: {
        model, messages: [{ role: "user", content: "hello" }], stream,
      }});
      if (removed) throw new Error("removed model was accepted");
      if (stream) {
        let count = 0;
        for await (const chunk of response) {
          if (!chunk) throw new Error("empty chunk");
          count++;
        }
        if (!count) throw new Error("empty stream");
      } else if (!response.choices[0].message.content.includes("mock")) {
        throw new Error("unexpected completion");
      }
    } catch (error) {
      if (!removed || error.statusCode !== 404) throw error;
    }
  }
}
console.log("PASS TypeScript SDK catalog transition: " + process.argv[2]);
