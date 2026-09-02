import { useMutation } from "@tanstack/react-query";
import { useState } from "react";

import { ConfirmDialog, reasonOf } from "@/components/ui/ConfirmDialog";
import { deleteAccount, type Account } from "@/lib/api";
import { formatCount } from "@/lib/format";

// DeleteAccountModal restates the account and the gateway API keys it holds.
// The gateway refuses a delete while any key remains, and that refusal is
// the one answer the operator needs next, so it lands in the dialog's error
// slot instead of a toast.
export function DeleteAccountModal({
  account,
  keyCount,
  onClose,
  onDeleted,
}: {
  account: Account;
  keyCount: number;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [error, setError] = useState("");
  const remove = useMutation({
    mutationFn: () => deleteAccount(account.id),
    onSuccess: onDeleted,
    onError: (problem) => setError(`Delete failed: ${reasonOf(problem)}`),
  });
  return (
    <ConfirmDialog
      title="Delete account"
      action="Delete account"
      error={error}
      pending={remove.isPending}
      onConfirm={() => remove.mutate()}
      onClose={onClose}
    >
      <p>
        Delete <strong className="text-text-1">{account.id}</strong>?{" "}
        {keyCount === 0
          ? "It holds no gateway API keys."
          : `It holds ${formatCount(keyCount)} gateway API key${keyCount === 1 ? "" : "s"}. The gateway refuses the delete while any key remains, so delete or reassign them first.`}
      </p>
    </ConfirmDialog>
  );
}
