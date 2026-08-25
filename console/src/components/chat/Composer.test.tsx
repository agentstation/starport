// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { Composer } from "./Composer";
import { messageContent } from "@/lib/chatStore";

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    hasCredential: () => true,
    listModels: async () => [
      {
        id: "openai/gpt-4o",
        name: "GPT-4o",
        architecture: { input_modalities: ["text", "image"] },
      },
      {
        id: "meta/llama-3.1-8b-instruct",
        name: "Llama 3.1 8B",
        architecture: { input_modalities: ["text"] },
      },
    ],
    listPresets: async () => [],
    listProviderCatalog: async () => [],
  };
});

afterEach(cleanup);

function mount(overrides: Partial<Parameters<typeof Composer>[0]> = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const onSend = vi.fn();
  render(
    <QueryClientProvider client={client}>
      <Composer
        draft="what is in this image?"
        onDraftChange={() => {}}
        onSend={onSend}
        streaming={false}
        onStop={() => {}}
        model="openai/gpt-4o"
        onModelChange={() => {}}
        favorites={new Set()}
        onToggleFavorite={() => {}}
        params={{
          system: "",
          temperature: null,
          maxTokens: null,
          order: "",
          only: "",
          ignore: "",
          sort: "",
          effort: "",
        }}
        onParamsChange={() => {}}
        pickerOpen={false}
        onPickerOpenChange={() => {}}
        {...overrides}
      />
    </QueryClientProvider>,
  );
  return { onSend };
}

test("the plus button owns attachments and no presets popover exists", async () => {
  mount();
  await waitFor(() => {
    expect(screen.getByLabelText("Attach image")).toBeTruthy();
  });
  expect(screen.queryByText("Presets")).toBeNull();
  expect(screen.getByLabelText("Attach images")).toBeTruthy();
});

test("attachments are disabled for a text-only model", async () => {
  mount({ model: "meta/llama-3.1-8b-instruct" });
  await waitFor(() => {
    const button = screen.getByLabelText(
      "This model does not accept image input",
    ) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
  });
});

test("attachments are disabled in compare mode", async () => {
  mount({ compareActive: true, compareModels: [], onCompareToggle: () => {} });
  const button = screen.getByLabelText(
    "Attachments are unavailable in compare mode",
  ) as HTMLButtonElement;
  expect(button.disabled).toBe(true);
});

test("an attached image renders as a chip and ships with send", async () => {
  const { onSend } = mount();
  await waitFor(() => {
    expect(screen.getByLabelText("Attach image")).toBeTruthy();
  });

  const file = new File(["fake-bytes"], "photo.png", { type: "image/png" });
  const input = screen.getByLabelText("Attach images");
  fireEvent.change(input, { target: { files: [file] } });

  await waitFor(() => {
    expect(screen.getByAltText("Attachment 1")).toBeTruthy();
  });

  fireEvent.click(screen.getByLabelText("Send message"));
  expect(onSend).toHaveBeenCalledTimes(1);
  const [text, images] = onSend.mock.calls[0] as [string, string[]];
  expect(text).toBe("what is in this image?");
  expect(images).toHaveLength(1);
  expect(images[0]).toMatch(/^data:image\/png;base64,/);
  // The chip clears after the send.
  await waitFor(() => {
    expect(screen.queryByAltText("Attachment 1")).toBeNull();
  });
});

test("a removed attachment does not ship", async () => {
  const { onSend } = mount();
  await waitFor(() => {
    expect(screen.getByLabelText("Attach image")).toBeTruthy();
  });
  const file = new File(["fake-bytes"], "photo.png", { type: "image/png" });
  fireEvent.change(screen.getByLabelText("Attach images"), {
    target: { files: [file] },
  });
  await waitFor(() => {
    expect(screen.getByAltText("Attachment 1")).toBeTruthy();
  });
  fireEvent.click(screen.getByLabelText("Remove attachment 1"));
  fireEvent.click(screen.getByLabelText("Send message"));
  expect(onSend).toHaveBeenCalledWith("what is in this image?", undefined);
});

// --- messageContent (request content-part contract)

test("messageContent keeps the legacy string shape without images", () => {
  expect(messageContent({ content: "hello" })).toBe("hello");
});

test("messageContent builds content parts with images", () => {
  expect(
    messageContent({ content: "look", images: ["data:image/png;base64,AA"] }),
  ).toEqual([
    { type: "text", text: "look" },
    { type: "image_url", image_url: { url: "data:image/png;base64,AA" } },
  ]);
});
