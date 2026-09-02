import { CalendarDays, X } from "lucide-react";
import { lazy, Suspense, useState } from "react";

import { INPUT_CLASS } from "@/components/ui/Form";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";

const Calendar = lazy(() =>
  import("@/components/ui/calendar").then((module) => ({ default: module.Calendar })),
);

// The field carries the date as an ISO day, so a form reads and writes
// the same shape the gateway does and no time zone shifts the chosen day.
const ISO_DAY = /^(\d{4})-(\d{2})-(\d{2})$/;

export function parseIsoDay(value: string): Date | undefined {
  const match = ISO_DAY.exec(value);
  if (!match) return undefined;
  return new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
}

export function formatIsoDay(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

const dayLabel = new Intl.DateTimeFormat(undefined, {
  year: "numeric",
  month: "short",
  day: "numeric",
});

// DateField is the shadcn date picker: a button that reads the chosen day
// and opens a month grid in a popover. It replaces the native date input,
// whose look and keyboard model differed by browser.
export function DateField({
  value,
  onChange,
  placeholder = "No date",
  clearable = true,
  id,
  ...control
}: {
  value: string;
  onChange: (next: string) => void;
  placeholder?: string;
  clearable?: boolean;
  id?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean | "true" | "false";
  "aria-required"?: boolean | "true" | "false";
}) {
  const [open, setOpen] = useState(false);
  const selected = parseIsoDay(value);

  return (
    <div className="flex items-center gap-1">
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          id={id}
          className={cn(
            INPUT_CLASS,
            "flex flex-1 items-center gap-2 text-left",
            !selected && "text-text-4",
          )}
          {...control}
        >
          <CalendarDays className="size-4 shrink-0 text-text-3" />
          <span className="flex-1 truncate">
            {selected ? dayLabel.format(selected) : placeholder}
          </span>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-auto p-0">
          <Suspense
            fallback={<div className="h-64 w-64 animate-pulse rounded-sm bg-bg-hover" />}
          >
            <Calendar
              mode="single"
              selected={selected}
              defaultMonth={selected}
              onSelect={(day) => {
                if (day) onChange(formatIsoDay(day));
                setOpen(false);
              }}
            />
          </Suspense>
        </PopoverContent>
      </Popover>
      {clearable && selected && (
        <button
          type="button"
          onClick={() => onChange("")}
          aria-label="Clear date"
          className="flex size-9 shrink-0 items-center justify-center rounded-sm text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1"
        >
          <X className="size-4" />
        </button>
      )}
    </div>
  );
}
