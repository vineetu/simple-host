// The one read-only value with a Copy button beside it (connector address,
// command line, key): Account's Connect apps and the team Website use it.
import { CopyButton } from "@kit/components/ui/copy";
import { Input } from "@kit/components/ui/input";

export function CopyRow({ value, label, button = "Copy" }: { value: string; label: string; button?: string }) {
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2">
      <Input readOnly value={value} aria-label={label} className="min-w-0 flex-[1_1_14rem] font-mono text-sm" onFocus={(e) => e.target.select()} />
      <CopyButton text={value} label={button} />
    </div>
  );
}
