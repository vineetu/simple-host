# Limits and keeping files small

## Numbers at a glance

- A site may be up to 300 MB on simple-host.app. **Archive limit** is 100 MB.
- Simple Host keeps 4 versions of a site; new websites (from the 2026-10-05 deployment) may total 200 MB including retained versions; 1 GB per account.
- Saved data: KV and SQLite share 10,000,000 bytes per site; files have a separate 10 MB allowance per site; one file upload from a page is under 1,000,000 bytes. `storage_get_usage` shows what is used.
- Photos: about 1600 px on the long side for full-width images, 800 px for cards; WebP or JPEG at quality 75 to 80; under about 300 KB each.

## Keep website files small

Before deploying, compress images and check the size of every file and the whole output folder. Read `who_am_i` (or `GET /v1/me`) for account file usage and `list_sites` (or `GET /v1/sites`) for site sizes. Simple Host normally keeps 4 versions, allows 200 MB including live files and retained versions for new websites from the 2026-10-05 deployment, and 1 GB per account. Earlier websites have no total cap; operator exceptions apply. The live copy is separate and counts too. The server checks after pruning, so replacing a site uses the resulting total. Saved data and storage resources have their own limits.

Shrink photos to about 1600 px wide, using WebP or JPEG at about 80% quality; phone photos are often 4–12 MB. Keep zip files, installers and videos elsewhere and link to them. Remove files you no longer use. Delete old sites you don't need.

Compress photos before the first deploy, not only after a refusal. If a deploy returns `site_total_too_large`, `account_storage_full` or `site_too_large`, follow its tips and reduce the files before retrying. Ask which old sites the person no longer needs before deleting any. Only accounts enabled by the operator may change the version count; other accounts get “Simple Host keeps your 4 latest versions”.

## Photos: shrink them before publishing

**Photos: shrink them before publishing.** Resize each photo to what the page
shows: at most about 1600 px on the long side for full-width images, about 800 px
for cards and thumbnails. Save as WebP or JPEG at quality 75–80; aim for under
~300 KB a photo and a few MB for the whole site. Never upload camera originals, or
screenshots saved as PNG, as photos; PNG or SVG is only for logos, icons and flat
graphics. Where you can run commands, one line per photo does it:

- ImageMagick: `magick in.jpg -resize '1600x1600>' -quality 80 out.jpg` (`convert` on version 6)
- macOS: `sips -Z 1600 -s format jpeg -s formatOptions 80 in.jpg --out out.jpg`
- Python Pillow: `python3 -c "from PIL import Image, ImageOps; im = ImageOps.exif_transpose(Image.open('in.jpg')); im.thumbnail((1600, 1600)); im.convert('RGB').save('out.jpg', quality=80)"`

Where you cannot, ask the person for smaller images, or pick web-sized versions.
PDFs such as tickets are fine as they are; compress a very large scanned PDF
where you can. A site may be up to 300 MB on simple-host.app, and every deploy
keeps a full copy of it as a saved version (`references/operations.md`
§Versions kept), so small files matter.
