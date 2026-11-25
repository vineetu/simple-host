import type { ComponentProps } from "react";
import { Tabs as T } from "radix-ui";
import { cn } from "@/lib/utils";

// Tabs, in one row that scrolls sideways on a narrow screen: labels on a dashed rule; the open one is underlined in teal.
export const Tabs = T.Root;

export function TabsList({ className, ...props }: ComponentProps<typeof T.List>) {
  return <T.List
      data-scroll-x
      className={cn(
        "-mx-4 flex gap-x-1.5 overflow-x-auto border-b-[1.5px] border-dashed border-ink-soft px-4 py-1 [scrollbar-width:none] md:mx-0 md:px-0 [&::-webkit-scrollbar]:hidden",
        className,
      )}
      {...props}
    />;
}

export function TabsTrigger({ className, ...props }: ComponentProps<typeof T.Trigger>) {
  return (
    <T.Trigger
      className={cn(
        "relative min-h-11 min-w-11 flex-none cursor-pointer rounded-tab px-2.5 text-base font-medium whitespace-nowrap text-ink-soft hover:text-ink md:px-3",
        "data-[state=active]:text-ink data-[state=active]:after:absolute data-[state=active]:after:inset-x-2.5 data-[state=active]:after:bottom-1.5 data-[state=active]:after:h-[3px] data-[state=active]:after:rounded-tag data-[state=active]:after:bg-accent",
        className,
      )}
      {...props}
    />
  );
}

export function TabsContent({ className, ...props }: ComponentProps<typeof T.Content>) {
  return <T.Content className={cn("pt-5 pb-14 focus-visible:outline-none", className)} {...props} />;
}
