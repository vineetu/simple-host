import { DropdownMenu as M } from "radix-ui";
import { cn } from "@/lib/utils";
import { Button } from "./button";

export type MenuItem =
  | { label: string; href: string; external?: boolean }
  | { label: string; onSelect: () => void; danger?: boolean }
  | { label: string; disabled: true }
  | { separator: true };

const item = "flex min-h-11 w-full cursor-pointer items-center rounded-sm px-3 text-left text-ink no-underline outline-none data-[highlighted]:bg-paper-lo";

/** Menu is a row's "more" button with its actions (Radix: arrow keys, Escape, focus return). */
export function Menu({ label, items }: { label: string; items: MenuItem[] }) {
  return (
    <M.Root modal={false}>
      <M.Trigger asChild>
        <Button variant="ghost" size="icon" aria-label={label}>
          <span aria-hidden="true">⋯</span>
        </Button>
      </M.Trigger>
      <M.Portal>
        <M.Content
          align="end"
          sideOffset={4}
          collisionPadding={8}
          className="z-50 min-w-56 rounded-tab border-[1.5px] border-ink-soft bg-card p-1 shadow-pop"
        >
          {items.map((it, i) => {
            if ("separator" in it) return <M.Separator key={i} className="my-1 h-px bg-rule" />;
            if ("disabled" in it)
              return (
                <M.Item key={i} disabled className={cn(item, "cursor-default text-ink-faint")}>
                  {it.label}
                </M.Item>
              );
            if ("href" in it)
              return (
                <M.Item key={i} asChild className={item}>
                  <a href={it.href} {...(it.external ? { target: "_blank", rel: "noopener" } : {})}>
                    {it.label}
                  </a>
                </M.Item>
              );
            return (
              <M.Item key={i} onSelect={it.onSelect} className={cn(item, it.danger && "text-bad")}>
                {it.label}
              </M.Item>
            );
          })}
        </M.Content>
      </M.Portal>
    </M.Root>
  );
}
