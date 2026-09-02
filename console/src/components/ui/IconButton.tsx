import type { ComponentProps } from "react";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

// IconButton is a square control whose only visible content is an icon.
// One label serves as the accessible name and as the tooltip, so the two
// can never drift apart and no button ships a native title attribute.
export function IconButton({
  label,
  className,
  children,
  ...props
}: { label: string } & Omit<ComponentProps<"button">, "aria-label" | "title">) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <button
            type="button"
            aria-label={label}
            className={cn(
              "inline-flex items-center justify-center transition-colors duration-150 ease-standard",
              className,
            )}
            {...props}
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}
