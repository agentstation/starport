import type { Model } from "@/lib/api";

// This module owns the chat media vocabulary in both directions: the
// attachments a reader adds to a turn, and the pictures and spoken answers a
// model sends back. The composer owns the controls, chatStore owns the
// conversation record, and both read the kinds, the catalog spelling of each
// one, and the wire shape from here.

export type AttachmentKind = "image" | "audio" | "document";

// ATTACHMENT_KINDS fixes the order the composer renders its controls in.
export const ATTACHMENT_KINDS: AttachmentKind[] = ["image", "audio", "document"];

// Attachment holds one file a reader added. Every kind is read as a data
// URL, so the bytes travel inside the conversation record and a retry of
// an older turn replays what the first attempt sent.
export type Attachment = {
  kind: AttachmentKind;
  url: string;
  name: string;
};

// GeneratedMedia is one non-text answer a model produced: a picture, or a
// spoken answer with its transcript beside it. Both hold a data URL, so a
// stored conversation replays them after a reload the way an attachment
// does, rather than pointing at a link the provider expires.
export type GeneratedMedia = {
  kind: "image" | "audio";
  url: string;
  transcript?: string;
};

// ContentPart is one element of a chat request content array. The three
// media shapes disagree about how they carry bytes, and each one follows
// the OpenAI request the gateway mirrors.
export type ContentPart =
  | { type: "text"; text: string }
  | { type: "image_url"; image_url: { url: string } }
  | { type: "input_audio"; input_audio: { data: string; format: string } }
  | { type: "file"; file: { filename: string; file_data: string } };

// CATALOG_MODALITY names each kind in the words the models API answers
// with. A document is the exception worth stating: the catalog records it
// as "pdf", so a control that looked for "document" would stay disabled
// against every model that reads one.
const CATALOG_MODALITY: Record<AttachmentKind, string> = {
  image: "image",
  audio: "audio",
  document: "pdf",
};

// ATTACHMENT_ACCEPT is the file picker filter for each kind. The document
// filter is narrow because "pdf" is the only document modality the
// catalog records.
export const ATTACHMENT_ACCEPT: Record<AttachmentKind, string> = {
  image: "image/*",
  audio: "audio/*",
  document: "application/pdf",
};

// modelAccepts reports whether the selected model reads one attachment
// kind. A model the catalog says nothing about accepts nothing here: the
// composer would rather disable a control than offer an attachment the
// provider refuses.
export function modelAccepts(
  model: Model | undefined,
  kind: AttachmentKind,
): boolean {
  const modalities = model?.architecture?.input_modalities ?? [];
  return modalities.includes(CATALOG_MODALITY[kind]);
}

// attachmentKindOf classifies one file, or answers null for a file no
// kind claims.
export function attachmentKindOf(file: File): AttachmentKind | null {
  if (file.type.startsWith("image/")) return "image";
  if (file.type.startsWith("audio/")) return "audio";
  if (file.type === "application/pdf") return "document";
  return null;
}

// readAttachment reads one file into an attachment. It answers null for a
// file no kind claims and for a read that fails, so the caller adds a
// chip only for media it can send.
export function readAttachment(file: File): Promise<Attachment | null> {
  const kind = attachmentKindOf(file);
  if (!kind) return Promise.resolve(null);
  return new Promise((resolve) => {
    const reader = new FileReader();
    reader.onload = () =>
      resolve(
        typeof reader.result === "string"
          ? { kind, url: reader.result, name: file.name }
          : null,
      );
    reader.onerror = () => resolve(null);
    reader.readAsDataURL(file);
  });
}

// attachmentPart encodes one attachment into the part the gateway decodes.
// An image and a document both carry a data URL, which keeps the media
// type with the bytes. Audio does not: its shape states the format in its
// own field and expects the base64 alone.
export function attachmentPart(attachment: Attachment): ContentPart {
  switch (attachment.kind) {
    case "image":
      return { type: "image_url", image_url: { url: attachment.url } };
    case "audio":
      return {
        type: "input_audio",
        input_audio: {
          data: base64Payload(attachment.url),
          format: audioFormat(attachment),
        },
      };
    case "document":
      return {
        type: "file",
        file: { filename: attachment.name, file_data: attachment.url },
      };
  }
}

// base64Payload strips the data URL header. The audio shape states its
// format in a separate field, so a header left in place would reach the
// provider as part of the bytes.
function base64Payload(url: string): string {
  const comma = url.indexOf(",");
  return comma >= 0 ? url.slice(comma + 1) : url;
}

// AUDIO_FORMAT_ALIASES maps the media type a browser reports onto the
// word the provider APIs use for the same encoding.
const AUDIO_FORMAT_ALIASES: Record<string, string> = {
  mpeg: "mp3",
  "x-wav": "wav",
  "x-m4a": "m4a",
  mp4: "m4a",
};

// audioFormat names the encoding for the audio shape, which asks for a
// word rather than a media type. The filename extension answers first,
// because it is what the reader chose. The media type answers for a file
// that arrives without an extension.
function audioFormat(attachment: Attachment): string {
  const dot = attachment.name.lastIndexOf(".");
  if (dot > 0) {
    return normalizeAudioFormat(attachment.name.slice(dot + 1).toLowerCase());
  }
  const mediaType = dataUrlMediaType(attachment.url);
  const slash = mediaType.indexOf("/");
  return normalizeAudioFormat(slash >= 0 ? mediaType.slice(slash + 1) : "");
}

function normalizeAudioFormat(word: string): string {
  return AUDIO_FORMAT_ALIASES[word] ?? word;
}

// dataUrlMediaType reads the media type out of a data URL header.
function dataUrlMediaType(url: string): string {
  const prefix = "data:";
  if (!url.startsWith(prefix)) return "";
  const header = url.slice(prefix.length);
  const end = header.search(/[;,]/);
  return end >= 0 ? header.slice(0, end) : header;
}
