// FilePick chooses files or a whole folder for a site: a box to tap or drop
// onto, then what was chosen, with a way to clear it.
import { useId, useState } from "react";
import { bytes, plural } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { FieldMessage } from "@/components/ui/input";

export function FilePick({ files, onChange, error }: { files: File[]; onChange: (f: File[]) => void; error?: string }) {
  const id = useId();
  const [over, setOver] = useState(false);
  const total = files.reduce((a, f) => a + f.size, 0);
  const pick =
    "inline-flex min-h-11 cursor-pointer items-center rounded-wobble border-[1.5px] border-ink-soft bg-card px-4 font-medium shadow-paper hover:bg-paper-lo has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-accent";
  return (
    <div className="mb-5">
      <p className="mb-1.5 text-sm font-medium text-ink-soft" id={id + "-label"}>
        Files
      </p>
      <div
        onDragOver={(e) => (e.preventDefault(), setOver(true))}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setOver(false);
          if (e.dataTransfer.files.length) onChange(Array.from(e.dataTransfer.files));
        }}
        className={cn(
          "rounded-field border-2 border-dashed px-4 py-5 text-center",
          over ? "border-accent bg-accent-soft" : error ? "border-bad" : "border-ink-soft",
        )}
      >
        {files.length ? (
          <p className="font-semibold">
            {plural(files.length, "file")} chosen <span className="font-normal text-ink-soft">· {bytes(total)}</span>
          </p>
        ) : (
          <p className="text-ink-soft">Drop files here, or choose them.</p>
        )}
        <div className="mt-3 flex flex-wrap justify-center gap-2">
          <label className={pick}>
            Choose files
            <input type="file" multiple className="sr-only" onChange={(e) => e.target.files && onChange(Array.from(e.target.files))} />
          </label>
          <label className={pick}>
            Choose a folder
            <input
              type="file"
              className="sr-only"
              // @ts-expect-error webkitdirectory is not in React's types; every current browser has it.
              webkitdirectory=""
              onChange={(e) => e.target.files && onChange(Array.from(e.target.files))}
            />
          </label>
          {files.length > 0 && (
            <Button variant="link" className="min-h-11 px-2" onClick={() => onChange([])}>
              Clear
            </Button>
          )}
        </div>
      </div>
      {error && <FieldMessage tone="bad">{error}</FieldMessage>}
    </div>
  );
}
