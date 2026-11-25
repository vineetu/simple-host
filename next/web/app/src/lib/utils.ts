import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/** cn joins class names; a later Tailwind class beats an earlier one of the same kind. */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
