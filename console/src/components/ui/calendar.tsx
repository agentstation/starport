import { ChevronLeft, ChevronRight } from "lucide-react";
import { DayPicker, type DayPickerProps } from "react-day-picker";

import { cn } from "@/lib/utils";

// Calendar is the shadcn month grid over react-day-picker, styled with the
// console tokens. DateField loads it on demand, so the grid and its date
// library stay out of the entry chunk until a form opens one.
export function Calendar({ className, classNames, ...props }: DayPickerProps) {
  return (
    <DayPicker
      showOutsideDays
      className={cn("p-2 text-sm", className)}
      classNames={{
        root: "relative",
        months: "relative flex",
        month: "flex flex-col gap-2",
        month_caption: "flex h-7 items-center justify-center px-8",
        caption_label: "text-sm font-medium text-text-1",
        nav: "absolute inset-x-0 top-0 flex h-7 items-center justify-between",
        button_previous:
          "flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1 disabled:opacity-40",
        button_next:
          "flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1 disabled:opacity-40",
        month_grid: "w-full border-collapse",
        weekdays: "flex",
        weekday: "w-8 text-center text-xs font-normal text-text-4",
        week: "mt-1 flex",
        day: "size-8 p-0 text-center",
        day_button:
          "size-8 rounded-xs text-sm text-text-2 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1",
        selected: "[&>button]:bg-accent [&>button]:text-accent-ink [&>button]:hover:bg-accent",
        today: "[&>button]:font-semibold [&>button]:text-accent-link",
        outside: "[&>button]:text-text-4",
        disabled: "[&>button]:opacity-40 [&>button]:hover:bg-transparent",
        hidden: "invisible",
        ...classNames,
      }}
      components={{
        Chevron: ({ orientation, className: chevronClass }) =>
          orientation === "left" ? (
            <ChevronLeft className={cn("size-4", chevronClass)} />
          ) : (
            <ChevronRight className={cn("size-4", chevronClass)} />
          ),
      }}
      {...props}
    />
  );
}
