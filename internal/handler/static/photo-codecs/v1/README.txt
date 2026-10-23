Photo decoders for Add photos

These are unmodified, pinned upstream files, fetched from the npm registry.
They are loaded only after native decoding fails. All runtime requests stay on
this server. decoder.js is the application's handwritten worker adapter.

libheif-js 1.23.5: libheif.js (91,255 bytes), libheif.wasm (1,463,506 bytes).
LGPL-3.0. LICENSE-libheif-js and LICENSE-libheif contain the license texts,
including GNU GPL v3 and the upstream wrapper's MIT notice.
Package: https://registry.npmjs.org/libheif-js/-/libheif-js-1.23.5.tgz
Source and package build scripts:
https://github.com/catdad-experiments/libheif-js/tree/1.23.5
Corresponding Wasm source and build scripts:
https://github.com/catdad-experiments/libheif-emscripten/tree/v1.23.5
The source build pins libheif and libde265; follow its README to rebuild the
Wasm files. Replace libheif.js and libheif.wasm together to use a modified,
interface-compatible decoder. The application loads them as separate files.

UTIF 3.1.0: UTIF.js (58,271 bytes). MIT, see LICENSE-UTIF.
Package and source:
https://registry.npmjs.org/utif/-/utif-3.1.0.tgz
https://github.com/photopea/UTIF.js

pako 1.0.11: pako.min.js (45,988 bytes). MIT and zlib, see LICENSE-pako and LICENSE-pako-zlib.
Used by UTIF for compressed TIFFs.
Package and source:
https://registry.npmjs.org/pako/-/pako-1.0.11.tgz
https://github.com/nodeca/pako/tree/1.0.11

Bump the /v1/ asset directory if any decoder or adapter changes after release;
these URLs are served with a one-year immutable cache header.
