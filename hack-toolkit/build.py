#!/usr/bin/env python3
"""Build deterministic Simple Hack kit ZIPs from maintained skill and metadata."""
import io
import json
from pathlib import Path
from zipfile import ZipFile, ZipInfo, ZIP_DEFLATED
from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parents[1]
KIT = ROOT / 'hack-toolkit'
META = KIT / 'metadata'
OUT = KIT / 'site' / 'downloads'
SKILL = ROOT / 'simple-host-website' / 'skills' / 'run-hackathon'
STAMP = (2026, 10, 1, 0, 0, 0)


def jsonbytes(path):
    return path.read_bytes()


def icon(size):
    im = Image.new('RGBA', (size, size), '#020617')
    draw = ImageDraw.Draw(im)
    r = round(size * 0.141)
    x = y = size // 2
    draw.ellipse((x-r, y-r, x+r, y+r), fill='#22d3ee')
    buf = io.BytesIO()
    im.save(buf, 'PNG', optimize=False)
    return buf.getvalue()


def skill_files(prefix):
    return {f'{prefix}/{path.relative_to(SKILL).as_posix()}': path.read_bytes()
            for path in sorted(SKILL.rglob('*')) if path.is_file()}


def archive(filename, contents):
    OUT.mkdir(parents=True, exist_ok=True)
    with ZipFile(OUT / filename, 'w') as z:
        for name, data in sorted(contents.items()):
            info = ZipInfo(name, STAMP)
            info.compress_type = ZIP_DEFLATED
            info.external_attr = 0o100644 << 16
            z.writestr(info, data, compresslevel=9)
    print(f'{filename}: {len(contents)} files')


def main():
    listing = json.loads((META / 'listing.json').read_text())
    review = json.loads((META / 'review.json').read_text())
    name = listing['name']
    version = listing['version']
    listing['$schema'] = 'https://agent-plugins.org/schemas/1.0.0/plugin.schema.json'
    common = skill_files(f'{name}/skills/run-hackathon')
    common[f'{name}/assets/logo.png'] = icon(512)
    common[f'{name}/assets/icon.png'] = icon(256)
    skills_only = dict(common)
    skills_only[f'{name}/plugin.json'] = (json.dumps(listing, indent=2, ensure_ascii=False)+'\n').encode()
    archive(f'simple-hack-skills-only-{version}.zip', skills_only)
    full = dict(common)
    full_listing = json.loads(json.dumps(listing))
    full_listing['extensions']['com.openai']['review'] = review
    full[f'{name}/plugin.json'] = (json.dumps(full_listing, indent=2, ensure_ascii=False)+'\n').encode()
    full[f'{name}/mcp.json'] = jsonbytes(META / 'mcp.json')
    archive(f'simple-hack-openai-{version}.zip', full)
    claude = skill_files(f'{name}/skills/run-hackathon')
    claude[f'{name}/.claude-plugin/plugin.json'] = jsonbytes(META / 'claude-plugin.json')
    claude[f'{name}/.mcp.json'] = jsonbytes(META / 'claude-mcp.json')
    claude[f'{name}/README.md'] = (KIT / 'CLAUDE-README.md').read_bytes()
    archive(f'simple-hack-claude-{version}.zip', claude)
    archive(f'run-hackathon-skill-{version}.zip', skill_files('run-hackathon'))
    (OUT / 'logo.png').write_bytes(common[f'{name}/assets/logo.png'])
    (OUT / 'icon.png').write_bytes(common[f'{name}/assets/icon.png'])
    (OUT / 'SUBMISSION.md').write_bytes((KIT / 'SUBMISSION.md').read_bytes())


if __name__ == '__main__':
    main()
