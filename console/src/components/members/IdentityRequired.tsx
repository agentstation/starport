import { UsersRound } from "lucide-react";

import { ExternalLink } from "@/components/ui/ExternalLink";

const GUIDE =
  "https://github.com/agentstation/starport/blob/main/docs/OPERATOR-GUIDE.md#identity";

// IdentityRequired is the empty state a people page shows when the
// deployment has no identity provider. Nobody can arrive, so the page
// offers the setup steps and no action that would fail.
export function IdentityRequired({ noun }: { noun: "members" | "teams" }) {
  return (
    <section
      data-testid="identity-required"
      aria-labelledby="identity-required-title"
      className="flex max-w-xl flex-col gap-3 rounded-md border border-border-1 bg-bg-panel p-5"
    >
      <div className="flex items-center gap-2">
        <UsersRound className="size-4 text-text-3" aria-hidden="true" />
        <h2 id="identity-required-title" className="text-sm font-medium text-text-1">
          No identity provider is configured
        </h2>
      </div>
      <p className="text-sm text-text-3">
        {noun === "members"
          ? "Members arrive through an identity provider, so this page stays empty until an operator configures one."
          : "A team holds members, and members arrive through an identity provider, so there is nobody to group until an operator configures one."}
      </p>
      <ol className="flex list-decimal flex-col gap-1.5 pl-5 text-sm text-text-2">
        <li>
          Set <code className="font-mono text-xs">STARPORT_IDENTITY_CALLBACK_BASE_URL</code> to
          the address people reach this gateway at.
        </li>
        <li>
          Set the OAuth client settings for Google or GitHub, or the WorkOS settings
          for SSO.
        </li>
        <li>Restart the gateway. The first-contact page then offers the identity grant.</li>
      </ol>
      <p className="text-sm text-text-3">
        The <ExternalLink href={GUIDE}>operator guide</ExternalLink> lists every setting.
      </p>
    </section>
  );
}
