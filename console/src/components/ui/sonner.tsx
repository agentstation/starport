import { CircleCheck, Info, OctagonX, TriangleAlert } from "lucide-react";
import { useSyncExternalStore } from "react";
import { Toaster as Sonner, type ToasterProps } from "sonner";

import { appliedTheme, onThemeChange } from "@/lib/theme";

// Toaster mounts the console's one toast region. DESIGN.md: bottom-right,
// one line, four seconds, a semantic left rule. The toast reads its colors
// from the role tokens, so it follows the theme like every other surface.
function Toaster(props: ToasterProps) {
  const theme = useSyncExternalStore(onThemeChange, appliedTheme, () => "dark" as const);
  return (
    <Sonner
      theme={theme}
      position="bottom-right"
      duration={4000}
      gap={8}
      offset={16}
      icons={{
        success: <CircleCheck className="size-4 text-success" />,
        info: <Info className="size-4 text-text-3" />,
        warning: <TriangleAlert className="size-4 text-warning" />,
        error: <OctagonX className="size-4 text-error" />,
      }}
      toastOptions={{
        unstyled: true,
        classNames: {
          toast:
            "flex w-[360px] max-w-[calc(100vw-2rem)] items-center gap-2.5 rounded-sm border border-border-2 border-l-2 bg-bg-raised px-3.5 py-2.5 text-sm text-text-1 shadow-overlay",
          success: "border-l-success",
          error: "border-l-error",
          warning: "border-l-warning",
          info: "border-l-border-2",
          icon: "flex shrink-0 items-center",
          title: "min-w-0 flex-1 truncate font-medium",
          content: "min-w-0 flex-1",
        },
      }}
      {...props}
    />
  );
}

export { Toaster };
