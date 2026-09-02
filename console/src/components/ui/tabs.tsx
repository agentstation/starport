import { Tabs as TabsPrimitive } from "@base-ui/react/tabs";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "@/lib/utils";

// Tabs are the DESIGN.md tab row: the `line` variant underlines the
// active tab in accent, the `chips` variant raises it as a bordered
// chip. Base UI owns the roving focus, arrow keys, and aria-controls.

function Tabs({ className, ...props }: TabsPrimitive.Root.Props) {
  return (
    <TabsPrimitive.Root
      data-slot="tabs"
      className={cn("group/tabs flex flex-col gap-3", className)}
      {...props}
    />
  );
}

const tabsListVariants = cva("group/tabs-list flex flex-wrap items-center", {
  variants: {
    variant: {
      line: "gap-1 border-b border-border-1",
      chips: "gap-2",
    },
  },
  defaultVariants: { variant: "line" },
});

function TabsList({
  className,
  variant = "line",
  ...props
}: TabsPrimitive.List.Props & VariantProps<typeof tabsListVariants>) {
  return (
    <TabsPrimitive.List
      data-slot="tabs-list"
      data-variant={variant}
      className={cn(tabsListVariants({ variant }), className)}
      {...props}
    />
  );
}

function TabsTrigger({ className, ...props }: TabsPrimitive.Tab.Props) {
  return (
    <TabsPrimitive.Tab
      data-slot="tabs-trigger"
      className={cn(
        "relative inline-flex items-center gap-1.5 whitespace-nowrap text-sm transition-colors duration-150 ease-standard outline-none focus-visible:ring-2 focus-visible:ring-accent/50 disabled:pointer-events-none disabled:opacity-50",
        "group-data-[variant=line]/tabs-list:h-9 group-data-[variant=line]/tabs-list:rounded-t-xs group-data-[variant=line]/tabs-list:px-3 group-data-[variant=line]/tabs-list:text-text-3 group-data-[variant=line]/tabs-list:hover:text-text-1 group-data-[variant=line]/tabs-list:data-active:text-text-1",
        "group-data-[variant=line]/tabs-list:after:absolute group-data-[variant=line]/tabs-list:after:inset-x-0 group-data-[variant=line]/tabs-list:after:-bottom-px group-data-[variant=line]/tabs-list:after:h-0.5 group-data-[variant=line]/tabs-list:after:bg-accent group-data-[variant=line]/tabs-list:after:opacity-0 group-data-[variant=line]/tabs-list:data-active:after:opacity-100",
        "group-data-[variant=chips]/tabs-list:h-9 group-data-[variant=chips]/tabs-list:rounded-sm group-data-[variant=chips]/tabs-list:border group-data-[variant=chips]/tabs-list:border-border-1 group-data-[variant=chips]/tabs-list:bg-bg-panel group-data-[variant=chips]/tabs-list:px-3 group-data-[variant=chips]/tabs-list:text-text-3 group-data-[variant=chips]/tabs-list:hover:border-border-2 group-data-[variant=chips]/tabs-list:hover:text-text-2 group-data-[variant=chips]/tabs-list:data-active:border-border-3 group-data-[variant=chips]/tabs-list:data-active:bg-bg-raised group-data-[variant=chips]/tabs-list:data-active:text-text-1",
        className,
      )}
      {...props}
    />
  );
}

function TabsContent({ className, ...props }: TabsPrimitive.Panel.Props) {
  return (
    <TabsPrimitive.Panel
      data-slot="tabs-content"
      className={cn("outline-none", className)}
      {...props}
    />
  );
}

export { Tabs, TabsContent, TabsList, TabsTrigger };
