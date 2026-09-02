// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { Toaster } from "@/components/ui/sonner";

import { FilesPanel } from "./FilesPanel";

// The file surface has three properties an operator depends on, and each one
// fails silently if it regresses. A missing column reads as a file that has no
// expiry. A refusal shown as "upload failed" hides which of three unrelated
// causes stopped it. And a total with no ceiling beside it reads as room the
// account may not have.

const gateway = vi.hoisted(() => ({
  files: [] as unknown[],
  hasMore: false,
  session: true,
  bound: null as number | null,
  uploadRefusal: null as unknown,
  deleted: [] as string[],
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    hasCredential: () => true,
    isCredentialRejected: () => false,
    onCredentialChange: () => () => {},
    hasSession: () => gateway.session,
    listFiles: async () => ({ files: gateway.files, hasMore: gateway.hasMore }),
    listAccounts: async () => [
      { id: actual.DEFAULT_ACCOUNT_ID, limits: { stored_bytes: gateway.bound } },
    ],
    uploadFile: async () => {
      if (gateway.uploadRefusal) throw gateway.uploadRefusal;
      return { id: "file-new", filename: "new.pdf" };
    },
    deleteFile: async (id: string) => {
      gateway.deleted.push(id);
      return {};
    },
  };
});

const REPORT = {
  id: "file-abc123",
  object: "file",
  bytes: 2048,
  created_at: 1_756_000_000,
  filename: "report.pdf",
  purpose: "user_data",
  expires_at: 1_756_086_400,
  status: "processed",
};

beforeEach(() => {
  gateway.files = [];
  gateway.hasMore = false;
  gateway.session = true;
  gateway.bound = null;
  gateway.uploadRefusal = null;
  gateway.deleted = [];
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <FilesPanel />
      <Toaster />
    </QueryClientProvider>,
  );
}

// FIL-V20, first statement. Every recorded field reaches the row. A file whose
// expiry never rendered would look permanent, and an operator would plan
// against a document the gateway is about to delete.
test("the list renders every field the gateway records", async () => {
  gateway.files = [REPORT];
  mount();

  const row = await waitFor(() => screen.getByTestId("file-row"));
  const text = row.textContent ?? "";
  expect(text).toContain("file-abc123");
  expect(text).toContain("report.pdf");
  expect(text).toContain("2.0 KB");
  expect(text).toContain("user_data");
  expect(text).toContain("processed");
  expect(text).toContain(new Date(1_756_086_400 * 1000).toLocaleString());
});

// A file the gateway keeps forever is a different fact from a file whose
// expiry the console failed to read, and the two must not render alike.
test("a file with no expiry says so rather than showing an empty cell", async () => {
  gateway.files = [{ ...REPORT, expires_at: undefined }];
  mount();

  const row = await waitFor(() => screen.getByTestId("file-row"));
  expect(row.textContent).toContain("never");
});

// FIL-V20, second statement. The gateway separates a purpose it does not
// serve, a retention window outside its range, and an account with no room
// left. Each needs a different next step, so the reason it gave is the message
// the reader gets.
test("a refused upload renders the reason the gateway gave", async () => {
  const { ApiError } = await import("@/lib/api");
  gateway.uploadRefusal = new ApiError(
    413,
    "The stored file limit for this account is full. Delete a file to make room.",
    null,
  );
  mount();

  await waitFor(() => screen.getByTestId("file-input"));
  fireEvent.change(screen.getByTestId("file-input"), {
    target: { files: [new File(["%PDF"], "big.pdf")] },
  });

  expect(
    await screen.findByText(
      "Upload refused: The stored file limit for this account is full. Delete a file to make room.",
    ),
  ).toBeTruthy();
});

// FIL-V20, third statement. A total alone answers a question nobody asked. The
// question is how much room is left, and only the pair answers it.
test("the total renders against the account bound", async () => {
  gateway.files = [REPORT, { ...REPORT, id: "file-two", bytes: 1024 }];
  gateway.bound = 10 * 1024;
  mount();

  await waitFor(() => expect(screen.getAllByTestId("file-row")).toHaveLength(2));
  const total = screen.getByTestId("stored-total");
  expect(total.textContent).toContain("3.0 KB of 10 KB stored");
  expect(
    screen.getByLabelText("Stored bytes against the account limit"),
  ).toBeTruthy();
});

// A pasted key may carry its own tighter bound, and the account limit needs an
// admin credential to read. A ceiling the panel cannot read is one it must not
// claim, so the total renders alone rather than against a number that is not
// the one enforcing the refusal.
test("without a session the total claims no bound", async () => {
  gateway.files = [REPORT];
  gateway.session = false;
  gateway.bound = 10 * 1024;
  mount();

  await waitFor(() => screen.getByTestId("file-row"));
  const total = screen.getByTestId("stored-total");
  expect(total.textContent).toContain("2.0 KB stored");
  expect(total.textContent).not.toContain("of");
});

// A capped list makes the sum a floor. Reporting it as the amount stored would
// tell an operator there is room the account does not have.
test("a capped list reports the total as a floor", async () => {
  gateway.files = [REPORT];
  gateway.hasMore = true;
  mount();

  await waitFor(() => screen.getByTestId("file-row"));
  const total = screen.getByTestId("stored-total");
  expect(total.textContent).toContain("At least");
  expect(total.textContent).toContain("the newest files only");
});

// Deletion is final: the gateway never rewrites a stored file and never reuses
// its identifier. A row action that deleted on the first click would make one
// misplaced click unrecoverable.
test("deleting asks first and sends the identifier only after the confirmation", async () => {
  gateway.files = [REPORT];
  mount();

  await waitFor(() => screen.getByTestId("file-row"));
  fireEvent.click(screen.getByLabelText("Delete report.pdf"));
  expect(gateway.deleted).toEqual([]);

  await waitFor(() => screen.getByRole("dialog"));
  fireEvent.click(screen.getByText("Delete"));
  await waitFor(() => expect(gateway.deleted).toEqual(["file-abc123"]));
});

// The status column is the file's lifecycle, so it renders as a pill, and the
// size column is the one number in the row, so it lines up on the right.
test("status renders as a lifecycle pill and size lines up on the right", async () => {
  gateway.files = [REPORT];
  mount();

  const row = await waitFor(() => screen.getByTestId("file-row"));
  const pill = screen.getByText("processed");
  expect(pill.getAttribute("class") ?? "").toContain("rounded-full");
  expect(pill.getAttribute("class") ?? "").toContain("bg-success-tint");

  const headers = Array.from(document.querySelectorAll("th"));
  expect(headers.length).toBe(7);
  expect(headers.every((th) => th.getAttribute("scope") === "col")).toBe(true);
  const size = headers.find((th) => th.textContent === "Size");
  expect(size?.getAttribute("class") ?? "").toContain("text-right");
  expect(row.querySelectorAll("td")[2]?.getAttribute("class") ?? "").toContain(
    "text-right",
  );
});
