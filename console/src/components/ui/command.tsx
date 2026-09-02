import { Command as CommandPrimitive, useCommandState } from "cmdk";
import { SearchIcon } from "lucide-react";
import { useEffect, useRef, type ComponentProps, type ReactNode } from "react";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

// Command is the cmdk surface at DESIGN.md Spotlight scale: a large
// borderless input over grouped rows. The dialog variant centers a
// ~640px panel near the top of the viewport.

function Command({
  className,
  ...props
}: ComponentProps<typeof CommandPrimitive>) {
  return (
    <CommandPrimitive
      data-slot="command"
      className={cn("flex size-full flex-col overflow-hidden text-text-1", className)}
      {...props}
    />
  );
}

function CommandDialog({
  title = "Command palette",
  description = "Search for a page, an entity, or an action.",
  children,
  className,
  showCloseButton = false,
  ...props
}: Omit<ComponentProps<typeof Dialog>, "children"> & {
  title?: string;
  description?: string;
  className?: string;
  showCloseButton?: boolean;
  children: ReactNode;
}) {
  return (
    <Dialog {...props}>
      <DialogContent
        aria-label={title}
        className={cn(
          "top-[14vh] max-h-[72vh] max-w-2xl translate-y-0 gap-0 overflow-hidden p-0",
          className,
        )}
        showCloseButton={showCloseButton}
      >
        <DialogHeader className="sr-only">
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {children}
      </DialogContent>
    </Dialog>
  );
}

function CommandInput({
  className,
  ref,
  ...props
}: ComponentProps<typeof CommandPrimitive.Input>) {
  const inputRef = useRef<HTMLInputElement>(null);
  const value = useCommandState((state) => state.value);

  // cmdk selects the first result when the search changes, but it reads the
  // selected item's id before that item re-renders, so aria-activedescendant
  // stays empty until the next arrow key. Re-sync it from the DOM after each
  // value change so a screen reader always hears the highlighted option.
  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    const root = input.closest("[cmdk-root]");
    const selected = root?.querySelector('[cmdk-item][aria-selected="true"]');
    if (selected?.id) {
      input.setAttribute("aria-activedescendant", selected.id);
    } else {
      input.removeAttribute("aria-activedescendant");
    }
  }, [value]);

  return (
    <div
      data-slot="command-input-wrapper"
      className="flex items-center gap-3 border-b border-border-1 px-4"
    >
      <SearchIcon className="size-5 shrink-0 text-text-4" />
      <CommandPrimitive.Input
        ref={(node) => {
          inputRef.current = node;
          if (typeof ref === "function") return ref(node);
          if (ref) ref.current = node;
        }}
        data-slot="command-input"
        className={cn(
          "h-14 w-full bg-transparent text-md text-text-1 outline-none placeholder:text-text-4 disabled:cursor-not-allowed disabled:opacity-50",
          className,
        )}
        {...props}
      />
    </div>
  );
}

function CommandList({
  className,
  ...props
}: ComponentProps<typeof CommandPrimitive.List>) {
  return (
    <CommandPrimitive.List
      data-slot="command-list"
      className={cn(
        "max-h-[58vh] scroll-py-1.5 overflow-x-hidden overflow-y-auto p-1.5 outline-none",
        className,
      )}
      {...props}
    />
  );
}

function CommandEmpty({
  className,
  ...props
}: ComponentProps<typeof CommandPrimitive.Empty>) {
  return (
    <CommandPrimitive.Empty
      data-slot="command-empty"
      className={cn("px-3 py-4 text-sm text-text-3", className)}
      {...props}
    />
  );
}

function CommandGroup({
  className,
  ...props
}: ComponentProps<typeof CommandPrimitive.Group>) {
  return (
    <CommandPrimitive.Group
      data-slot="command-group"
      className={cn(
        "overflow-hidden **:[[cmdk-group-heading]]:px-3 **:[[cmdk-group-heading]]:pt-2.5 **:[[cmdk-group-heading]]:pb-1 **:[[cmdk-group-heading]]:text-[10px] **:[[cmdk-group-heading]]:font-medium **:[[cmdk-group-heading]]:uppercase **:[[cmdk-group-heading]]:tracking-[0.08em] **:[[cmdk-group-heading]]:text-text-4",
        className,
      )}
      {...props}
    />
  );
}

function CommandSeparator({
  className,
  ...props
}: ComponentProps<typeof CommandPrimitive.Separator>) {
  return (
    <CommandPrimitive.Separator
      data-slot="command-separator"
      className={cn("my-1 h-px bg-border-1", className)}
      {...props}
    />
  );
}

function CommandItem({
  className,
  ...props
}: ComponentProps<typeof CommandPrimitive.Item>) {
  return (
    <CommandPrimitive.Item
      data-slot="command-item"
      className={cn(
        "flex h-10 cursor-default items-center gap-3 rounded-sm px-3 text-left text-sm outline-none select-none data-[disabled=true]:pointer-events-none data-[disabled=true]:opacity-50 data-selected:bg-bg-hover [&_svg]:pointer-events-none [&_svg]:shrink-0",
        className,
      )}
      {...props}
    />
  );
}

function CommandShortcut({ className, ...props }: ComponentProps<"span">) {
  return (
    <span
      data-slot="command-shortcut"
      className={cn("ml-auto text-xs tracking-widest text-text-4", className)}
      {...props}
    />
  );
}

export {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
  CommandShortcut,
};
