import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2, Upload } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";

import { GhostButton, PrimaryButton } from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import { Modal } from "@/components/ui/Modal";
import {
  accessMessage,
  ApiError,
  DEFAULT_ACCOUNT_ID,
  deleteFile,
  hasSession,
  uploadFile,
  type StoredFile,
} from "@/lib/api";
import { queries } from "@/lib/queries";
import { formatBytes, formatUnixTime } from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

// A stored file belongs to the account whose credential asked for it. The
// routes scope every answer to the caller, so this panel shows one account's
// files and never a deployment-wide list. An operator reading another account's
// storage reads it with that account's credential.

// PURPOSES are the purposes this gateway serves. The gateway is the authority:
// it refuses one it does not serve and names the set it accepts, and that
// refusal is what the reader sees rather than a message this file invents.
const PURPOSES = ["user_data", "vision"] as const;

type Purpose = (typeof PURPOSES)[number];

// storedBytes sums what the listed files hold.
function storedBytes(files: StoredFile[]): number {
  return files.reduce((total, file) => total + (Number(file.bytes) || 0), 0);
}

// StoredTotal reports the room this account has used against the room it has.
//
// The bound comes from the account limit, which only an admin credential can
// read, and only a console session resolves to a known account. With a pasted
// key the total renders alone: a key may carry its own tighter bound, and a
// ceiling this panel cannot read is one it must not claim.
export function StoredTotal({
  files,
  hasMore,
  bound,
}: {
  files: StoredFile[];
  hasMore: boolean;
  bound: number | null;
}) {
  const used = storedBytes(files);
  const share = bound && bound > 0 ? Math.min(used / bound, 1) : null;
  return (
    <div className="flex flex-col gap-1.5" data-testid="stored-total">
      <p className="text-sm text-text-2">
        {hasMore ? "At least " : ""}
        <span className="tabular-nums">{formatBytes(used)}</span>
        {bound === null ? (
          " stored"
        ) : (
          <>
            {" of "}
            <span className="tabular-nums">{formatBytes(bound)}</span>
            {" stored"}
          </>
        )}
      </p>
      {share !== null && (
        <div
          className="h-1 w-56 overflow-hidden rounded-xs bg-bg-raised"
          role="progressbar"
          aria-label="Stored bytes against the account limit"
          aria-valuemin={0}
          aria-valuemax={bound ?? undefined}
          aria-valuenow={used}
        >
          <div
            className={`h-full ${share >= 1 ? "bg-error" : "bg-accent-link"}`}
            style={{ width: `${Math.round(share * 100)}%` }}
          />
        </div>
      )}
      {hasMore && (
        <p className="text-xs text-text-4">
          The list is capped, so the total counts the newest files only.
        </p>
      )}
    </div>
  );
}

// refusalText reads the reason the gateway gave.
//
// A refused upload is the one place a generic message costs the reader the
// answer: the gateway distinguishes a purpose it does not serve, a retention
// window outside its range, and an account with no room left, and each of the
// three needs a different next step.
function refusalText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.needsKey || error.forbidden) return accessMessage(error, "files:write");
    return error.message;
  }
  return error instanceof Error ? error.message : String(error);
}

export function FilesPanel() {
  const access = useGatewayAccess();
  const queryClient = useQueryClient();
  const chooser = useRef<HTMLInputElement>(null);
  const [purpose, setPurpose] = useState<Purpose>("user_data");
  const [confirming, setConfirming] = useState<StoredFile | null>(null);
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(
    null,
  );

  const files = useQuery({
    ...queries.files(),
    enabled: access,
  });

  // The account limit is readable only through the admin surface, and only a
  // console session names the account it applies to.
  const accounts = useQuery({
    ...queries.accounts(),
    enabled: access && hasSession(),
  });
  const bound =
    (accounts.data ?? []).find((account) => account.id === DEFAULT_ACCOUNT_ID)
      ?.limits?.stored_bytes ?? null;

  const upload = useMutation({
    mutationFn: (file: File) => uploadFile(file, purpose),
    onSuccess: async (stored) => {
      setNotice({ text: `Uploaded ${stored.filename}` });
      await queryClient.invalidateQueries({ queryKey: queries.files().queryKey });
    },
    onError: (error) =>
      setNotice({ text: `Upload refused: ${refusalText(error)}`, error: true }),
  });

  const remove = useMutation({
    mutationFn: (file: StoredFile) => deleteFile(file.id),
    onSuccess: async (_result, file) => {
      setConfirming(null);
      setNotice({ text: `Deleted ${file.filename}` });
      await queryClient.invalidateQueries({ queryKey: queries.files().queryKey });
    },
    onError: (error) => {
      setConfirming(null);
      setNotice({ text: `Delete failed: ${refusalText(error)}`, error: true });
    },
  });

  const rows = files.data?.files ?? [];

  let body: ReactNode;
  if (files.error) {
    body = (
      <p className="text-base text-text-3">
        {files.error instanceof ApiError && files.error.needsKey
          ? accessMessage(files.error, "files:read")
          : `Failed to load files: ${files.error.message}`}
      </p>
    );
  } else if (files.isPending) {
    body = <p className="text-base text-text-3">Loading files…</p>;
  } else if (rows.length === 0) {
    body = (
      <p className="text-base text-text-3">
        This account stores no files. Upload one to reference it from a chat
        request by its identifier.
      </p>
    );
  } else {
    body = (
      <div className="overflow-x-auto rounded-md border border-border-1 bg-bg-panel">
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-border-1 text-left text-xs font-medium text-text-3">
              <th className="px-4 py-2.5">File</th>
              <th className="px-4 py-2.5">Filename</th>
              <th className="px-4 py-2.5">Size</th>
              <th className="px-4 py-2.5">Purpose</th>
              <th className="px-4 py-2.5">Status</th>
              <th className="px-4 py-2.5">Expires</th>
              <th className="px-4 py-2.5" />
            </tr>
          </thead>
          <tbody>
            {rows.map((file) => (
              <tr
                key={file.id}
                data-testid="file-row"
                className="border-b border-border-1 last:border-0"
              >
                <td className="px-4 py-2 font-mono text-xs text-text-2">
                  {file.id}
                </td>
                <td className="px-4 py-2 text-text-2">{file.filename}</td>
                <td className="px-4 py-2 tabular-nums text-text-2">
                  {formatBytes(file.bytes)}
                </td>
                <td className="px-4 py-2 text-xs text-text-3">{file.purpose}</td>
                <td className="px-4 py-2 text-xs text-text-3">{file.status}</td>
                <td className="px-4 py-2 text-xs text-text-3">
                  {file.expires_at ? formatUnixTime(file.expires_at) : "never"}
                </td>
                <td className="px-4 py-2">
                  <div className="flex items-center justify-end">
                    <button
                      type="button"
                      onClick={() => setConfirming(file)}
                      aria-label={`Delete ${file.filename}`}
                      className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-error-tint hover:text-error"
                    >
                      <Trash2 className="size-3.5" />
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <StoredTotal
          files={rows}
          hasMore={files.data?.hasMore === true}
          bound={bound}
        />
        <div className="flex items-center gap-2">
          <label className="sr-only" htmlFor="file-purpose">
            Purpose
          </label>
          <Select
            id="file-purpose"
            value={purpose}
            onChange={(event) => setPurpose(event.target.value as Purpose)}
          >
            {PURPOSES.map((option) => (
              <option key={option} value={option}>
                {option}
              </option>
            ))}
          </Select>
          <input
            ref={chooser}
            type="file"
            data-testid="file-input"
            className="hidden"
            onChange={(event) => {
              const chosen = event.target.files?.[0];
              event.target.value = "";
              if (chosen) upload.mutate(chosen);
            }}
          />
          <PrimaryButton
            onClick={() => chooser.current?.click()}
            disabled={upload.isPending}
          >
            <Upload className="size-4" />
            {upload.isPending ? "Uploading…" : "Upload file"}
          </PrimaryButton>
        </div>
      </div>
      {notice && (
        <p
          data-testid="file-notice"
          className={`text-sm ${notice.error ? "text-error" : "text-success"}`}
        >
          {notice.text}
        </p>
      )}
      {body}
      {confirming && (
        <Modal
          title="Delete this file?"
          onClose={() => setConfirming(null)}
          footer={
            <>
              <GhostButton onClick={() => setConfirming(null)}>
                Cancel
              </GhostButton>
              <PrimaryButton
                onClick={() => remove.mutate(confirming)}
                disabled={remove.isPending}
              >
                Delete
              </PrimaryButton>
            </>
          }
        >
          <p className="text-sm text-text-2">
            The gateway deletes the bytes of{" "}
            <span className="font-mono text-xs">{confirming.filename}</span> and
            never reuses{" "}
            <span className="font-mono text-xs">{confirming.id}</span>. A request
            that names it after this reads as a file that never existed.
          </p>
        </Modal>
      )}
    </div>
  );
}
