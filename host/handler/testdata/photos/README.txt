Canvas regression samples for the Add photos JPEG/WebP header validator.
Generated 2026-10-23 by scripts/e2e-photo-safety.cjs using Playwright:
Chromium 156.0.8078.4, Firefox 157.0, WebKit 27.2 (Safari's engine).
All three supply both JPEG and WebP. These are unmodified canvas
outputs of the same 32x24 two-colour drawing. No third-party image content.
WebKit is an engine check, not a run of the Safari application on macOS/iOS.

Deflate regression: deflate-expansion.tiff is a 2,039-byte generated TIFF with
a 1x1, 8-bit grayscale header and a zlib strip expanding to 1 MiB of zeroes.
It must fail at the strip bound without accumulating the expanded output.
