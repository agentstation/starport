import { useEffect, useState } from "react";

import { LoadFailed } from "@/components/ui/LoadFailed";
import { identityBeginPath, identityProviders } from "@/lib/api";

// labels maps the provider names the gateway serves to how people write them.
// A provider outside the map still renders, capitalized, so a new provider on
// the gateway side does not need a console release to be reachable.
const labels: Record<string, string> = {
  google: "Google",
  github: "GitHub",
  workos: "SSO",
};

export function providerLabel(name: string): string {
  return labels[name] ?? name.charAt(0).toUpperCase() + name.slice(1);
}

// IdentitySignIn renders the sign-in choices an operator configured, and
// nothing at all when they configured none. It asks the gateway rather than
// assuming: the first-contact page is the same page on every deployment, and
// this section is the one part of it that only some deployments have. A
// failed question is not an empty answer: it renders as a failure with a
// retry, so a gateway that is down never looks like one with no providers.
//
// It sits below the token form on purpose. The machine-local token is the
// path that always works; signing in through a provider is the path that says
// who you are, and it exists only where an operator set one up.
//
// Each choice is an anchor, not a button with a handler: the OAuth dance is a
// chain of redirects the browser must follow, so the navigation has to leave
// the app entirely and come back with the session cookie already set.
export function IdentitySignIn() {
  const [providers, setProviders] = useState<string[]>([]);
  const [failure, setFailure] = useState<unknown>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setFailure(null);
    identityProviders().then(
      (list) => {
        if (!cancelled) setProviders(list);
      },
      (error: unknown) => {
        if (!cancelled) setFailure(error ?? new Error("Sign-in options request failed"));
      },
    );
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  if (failure) {
    return (
      <LoadFailed
        what="sign-in options"
        error={failure}
        onRetry={() => setAttempt((current) => current + 1)}
      />
    );
  }
  if (providers.length === 0) return null;

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <span aria-hidden="true" className="h-px flex-1 bg-border-1" />
        <span className="text-sm text-text-3">or sign in</span>
        <span aria-hidden="true" className="h-px flex-1 bg-border-1" />
      </div>
      <div className="flex flex-col gap-2">
        {providers.map((name) => (
          <a
            key={name}
            href={identityBeginPath(name)}
            className="flex h-9 items-center justify-center rounded-sm border border-border-2 px-4 text-sm text-text-2 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1"
          >
            Sign in with {providerLabel(name)}
          </a>
        ))}
      </div>
    </section>
  );
}
