// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { Composer } from "./Composer";
import type { Attachment } from "@/lib/attachments";
import { messageContent } from "@/lib/chatStore";

// MODELS differ only in what they read. Every attachment control follows
// its own modality, so a model that reads one kind and not another is the
// case that separates a per-kind gate from a single flag.
const MODELS = vi.hoisted(() => [
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
  {
    id: "google/gemini-2.5-flash",
    name: "Gemini 2.5 Flash",
    // The catalog names a document "pdf", which is the word the composer
    // has to look for.
    architecture: { input_modalities: ["text", "image", "audio", "pdf"] },
  },
]);

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    hasCredential: () => true,
    listModels: async () => MODELS,
    listPresets: async () => [],
    listProviderCatalog: async () => [],
  };
});

afterEach(cleanup);

function mount(overrides: Partial<Parameters<typeof Composer>[0]> = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  // The list is seeded rather than awaited. Every control is disabled
  // while the query is in flight, so a test that waits for a disabled
  // control passes before the answer arrives and proves nothing.
  client.setQueryData(["models"], MODELS);
  const onSend = vi.fn();
  const { model: initialModel, ...rest } = overrides;
  const tree = (model: string) => (
    <QueryClientProvider client={client}>
      <Composer
        draft="what is in this image?"
        onDraftChange={() => {}}
        onSend={onSend}
        streaming={false}
        onStop={() => {}}
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
        {...rest}
        model={model}
      />
    </QueryClientProvider>
  );
  const view = render(tree(initialModel ?? "openai/gpt-4o"));
  // rerender switches the selected model on the mounted composer, which
  // is the only way to observe what a switch does to a live draft.
  const rerender = (next: string) => view.rerender(tree(next));
  return { onSend, rerender };
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
    "Image attachments are unavailable in compare mode",
  ) as HTMLButtonElement;
  expect(button.disabled).toBe(true);
});

// Each control follows its own modality. A single "can attach" flag read
// the image modality alone, so an audio control behind it would have
// offered audio to every model that can see.
test("the audio control follows the audio modality, not the image one", async () => {
  mount({ model: "openai/gpt-4o" });
  expect(
    (screen.getByLabelText("Attach image") as HTMLButtonElement).disabled,
  ).toBe(false);
  expect(
    (
      screen.getByLabelText(
        "This model does not accept audio input",
      ) as HTMLButtonElement
    ).disabled,
  ).toBe(true);

  cleanup();
  mount({ model: "google/gemini-2.5-flash" });
  expect(
    (screen.getByLabelText("Attach audio") as HTMLButtonElement).disabled,
  ).toBe(false);
  expect(
    (screen.getByLabelText("Attach document") as HTMLButtonElement).disabled,
  ).toBe(false);
});

test("every attachment control is disabled for a text-only model", () => {
  mount({ model: "meta/llama-3.1-8b-instruct" });
  for (const label of [
    "This model does not accept image input",
    "This model does not accept audio input",
    "This model does not accept document input",
  ]) {
    expect((screen.getByLabelText(label) as HTMLButtonElement).disabled).toBe(true);
  }
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
  const [text, attachments] = onSend.mock.calls[0] as [string, Attachment[]];
  expect(text).toBe("what is in this image?");
  expect(attachments).toHaveLength(1);
  const [image] = attachments;
  expect(image?.kind).toBe("image");
  expect(image?.url).toMatch(/^data:image\/png;base64,/);
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

test("an attached document names the file and ships as a document", async () => {
  const { onSend } = mount({ model: "google/gemini-2.5-flash" });
  const file = new File(["%PDF-1.4"], "report.pdf", {
    type: "application/pdf",
  });
  fireEvent.change(screen.getByLabelText("Attach documents"), {
    target: { files: [file] },
  });

  // A document has no thumbnail, so the chip carries the filename.
  await waitFor(() => {
    expect(screen.getByText("report.pdf")).toBeTruthy();
  });

  fireEvent.click(screen.getByLabelText("Send message"));
  const [, attachments] = onSend.mock.calls[0] as [string, Attachment[]];
  expect(attachments).toHaveLength(1);
  const [document] = attachments;
  expect(document?.kind).toBe("document");
  expect(document?.name).toBe("report.pdf");
  expect(document?.url).toMatch(/^data:application\/pdf;base64,/);
});

// Switching models mid-draft is the case a per-kind gate has to survive.
// An audio file left attached to a model that reads none reaches the
// provider as a request that can never succeed.
test("a model switch drops the attachments the new model cannot read", async () => {
  const { rerender } = mount({ model: "google/gemini-2.5-flash" });
  fireEvent.change(screen.getByLabelText("Attach audio files"), {
    target: {
      files: [new File(["RIFF"], "clip.wav", { type: "audio/wav" })],
    },
  });
  fireEvent.change(screen.getByLabelText("Attach images"), {
    target: {
      files: [new File(["fake-bytes"], "photo.png", { type: "image/png" })],
    },
  });
  await waitFor(() => {
    expect(screen.getByText("clip.wav")).toBeTruthy();
    expect(screen.getByAltText("Attachment 2")).toBeTruthy();
  });

  // GPT-4o reads images and no audio, so the image chip survives.
  rerender("openai/gpt-4o");
  await waitFor(() => {
    expect(screen.queryByText("clip.wav")).toBeNull();
  });
  expect(screen.getByAltText("Attachment 1")).toBeTruthy();
});

// --- messageContent (request content-part contract)

test("messageContent keeps the legacy string shape without images", () => {
  expect(messageContent({ content: "hello" })).toBe("hello");
});

// A conversation stored before an attachment carried a kind holds images
// alone. Those records are the reader's own history, so the read path
// still has to turn them into image parts.
test("messageContent still reads a stored images record", () => {
  expect(
    messageContent({ content: "look", images: ["data:image/png;base64,AA"] }),
  ).toEqual([
    { type: "text", text: "look" },
    { type: "image_url", image_url: { url: "data:image/png;base64,AA" } },
  ]);
});

test("messageContent builds content parts with attachments", () => {
  expect(
    messageContent({
      content: "look",
      attachments: [
        { kind: "image", url: "data:image/png;base64,AA", name: "a.png" },
      ],
    }),
  ).toEqual([
    { type: "text", text: "look" },
    { type: "image_url", image_url: { url: "data:image/png;base64,AA" } },
  ]);
});

// The three shapes disagree about how they carry bytes. A document keeps
// its data URL under file_data, and audio hands over the base64 alone
// beside a format word, so a shared encoder would break one of the two.
test("a document attachment becomes the file part shape", () => {
  expect(
    messageContent({
      content: "summarize",
      attachments: [
        {
          kind: "document",
          url: "data:application/pdf;base64,JVBERg==",
          name: "report.pdf",
        },
      ],
    }),
  ).toEqual([
    { type: "text", text: "summarize" },
    {
      type: "file",
      file: {
        filename: "report.pdf",
        file_data: "data:application/pdf;base64,JVBERg==",
      },
    },
  ]);
});

test("an audio attachment ships raw base64 and a format word", () => {
  expect(
    messageContent({
      content: "transcribe",
      attachments: [
        { kind: "audio", url: "data:audio/wav;base64,UklGRg==", name: "clip.wav" },
      ],
    }),
  ).toEqual([
    { type: "text", text: "transcribe" },
    { type: "input_audio", input_audio: { data: "UklGRg==", format: "wav" } },
  ]);
});

// A browser reports an MP3 as audio/mpeg, and no provider accepts "mpeg"
// as a format. A file that arrives without an extension has only the
// media type to name it by.
test("the audio format falls back to the media type", () => {
  expect(
    messageContent({
      content: "transcribe",
      attachments: [
        { kind: "audio", url: "data:audio/mpeg;base64,SUQz", name: "recording" },
      ],
    }),
  ).toEqual([
    { type: "text", text: "transcribe" },
    { type: "input_audio", input_audio: { data: "SUQz", format: "mp3" } },
  ]);
});
