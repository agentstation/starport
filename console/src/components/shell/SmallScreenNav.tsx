import { Menu, Search as SearchIcon } from "lucide-react";
import type { ReactNode } from "react";

import { IconButton } from "@/components/ui/IconButton";
import { Sheet, SheetContent, SheetTrigger } from "@/components/ui/sheet";

// SmallScreenNav is the top bar the shell renders below the breakpoint: a
// trigger for the navigation sheet, the brand, and a search button. The
// shell loads it lazily, so a desktop first paint never downloads the
// sheet machinery. The sheet body and the brand arrive as elements, which
// keeps this module free of an import back into the shell.
export default function SmallScreenNav({
  open,
  onOpenChange,
  brand,
  body,
  onSearch,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  brand: ReactNode;
  body: ReactNode;
  onSearch: () => void;
}) {
  return (
    <header className="sticky top-0 z-30 flex h-12 shrink-0 items-center gap-1.5 border-b border-border-1 bg-bg-panel px-1">
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetTrigger
          render={
            <IconButton
              label="Open navigation"
              className="size-11 rounded-sm text-text-2 hover:bg-bg-hover hover:text-text-1"
            />
          }
        >
          <Menu aria-hidden="true" className="size-5" />
        </SheetTrigger>
        <SheetContent side="left" aria-label="Navigation" className="w-64 overflow-y-auto p-0">
          {body}
        </SheetContent>
      </Sheet>
      {brand}
      <IconButton
        label="Search"
        onClick={onSearch}
        className="ml-auto size-11 rounded-sm text-text-2 hover:bg-bg-hover hover:text-text-1"
      >
        <SearchIcon aria-hidden="true" className="size-4" />
      </IconButton>
    </header>
  );
}
