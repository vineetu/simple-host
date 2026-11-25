import type { ComponentProps } from "react";
import { Slot } from "radix-ui";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

// Buttons with the old hand-drawn edge, in the system font. Every size is at least 44 px tall.
const buttonVariants = cva(
  "inline-flex shrink-0 cursor-pointer items-center justify-center gap-2 text-center no-underline transition-colors disabled:cursor-default disabled:opacity-55",
  {
    variants: {
      variant: {
        // The one filled button of a screen, card or dialog: teal.
        primary:
          "rounded-wobble bg-accent text-base font-semibold text-on-accent shadow-paper hover:bg-accent-hover hover:text-on-accent hover:shadow-paper-sm",
        secondary:
          "rounded-wobble border-[1.5px] border-ink-soft bg-card text-base font-medium text-ink shadow-paper hover:bg-paper-lo hover:text-ink hover:shadow-paper-sm",
        danger:
          "rounded-wobble border-[1.5px] border-bad bg-card text-base font-medium text-bad shadow-paper hover:bg-paper-lo hover:text-bad",
        // A word inside a sentence ("Change").
        link: "inline rounded-sm bg-transparent p-0 text-accent underline underline-offset-2 hover:text-accent-hover",
        // Google's own sign-in button look (their brand rules: white, grey edge).
        google: "rounded-[24px] border border-ink-soft bg-white font-sans font-medium text-ink hover:bg-white hover:text-ink",
        // A bare control in a toolbar or row (the row menu).
        ghost: "rounded-field border-[1.5px] border-rule bg-card text-ink hover:border-accent",
      },
      size: {
        lg: "min-h-12 w-full px-4",
        md: "min-h-11 px-4",
        icon: "size-11 text-lg leading-none",
        inline: "",
      },
    },
    compoundVariants: [{ variant: "link", className: "min-h-0" }],
    defaultVariants: { variant: "secondary", size: "md" },
  },
);

export type ButtonProps = ComponentProps<"button"> & VariantProps<typeof buttonVariants> & { asChild?: boolean };

export function Button({ className, variant, size, asChild = false, type, ...props }: ButtonProps) {
  const Comp = asChild ? Slot.Root : "button";
  return (
    <Comp
      data-slot="button"
      type={asChild ? undefined : (type ?? "button")}
      className={cn(buttonVariants({ variant, size: variant === "link" ? "inline" : size }), className)}
      {...props}
    />
  );
}
