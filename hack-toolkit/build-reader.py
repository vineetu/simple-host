#!/usr/bin/env python3
"""Publish the reviewed Simple Hack skill snapshot as verbatim, readable text."""
from html import escape
from pathlib import Path
from shutil import copyfile, rmtree

KIT = Path(__file__).resolve().parent
SKILLS = KIT / "skills"
SITE = KIT / "site"
DEST = SITE / "skills"
NAMES = (
    ("run-hackathon", "Run a hackathon"),
    ("join-hackathon", "Join a hackathon"),
    ("judge-hackathon", "Judge a hackathon"),
    ("website-deploy-builder", "Website Deploy Builder"),
    ("website-deploy", "Website Deploy"),
)


def files_for(name):
    root = SKILLS / name
    paths = [Path("SKILL.md")]
    paths.extend(sorted(p.relative_to(root) for p in (root / "references").glob("*.md")))
    paths.extend(sorted(p.relative_to(root) for p in (root / "agents").glob("*.yaml")))
    return paths


def anchor(name, path):
    return name + "-" + path.as_posix().replace("/", "-").replace(".", "-")


def build():
    # The reader is a complete mirror of the maintained snapshot, so a removed
    # reference must not linger as a public raw file in a later release.
    if DEST.exists():
        rmtree(DEST)
    DEST.mkdir(parents=True, exist_ok=True)
    nav = []
    sections = []
    for name, title in NAMES:
        paths = files_for(name)
        nav.append(f'<a class="role" href="#{anchor(name, paths[0])}">{escape(title)}</a>')
        links = []
        for path in paths:
            source = SKILLS / name / path
            target = DEST / name / path
            target.parent.mkdir(parents=True, exist_ok=True)
            copyfile(source, target)
            label = path.as_posix()
            links.append(f'<a href="#{anchor(name, path)}">{escape(label)}</a>')
        parts = [f'<section class="skill" aria-labelledby="{name}-title">',
                 f'<h2 id="{name}-title">{escape(title)}</h2>',
                 '<nav class="files" aria-label="Files for '+escape(title)+'">'+" · ".join(links)+'</nav>']
        for path in paths:
            label = path.as_posix()
            url = f'{name}/{label}'
            content = (SKILLS / name / path).read_text(encoding="utf-8")
            parts.extend((
                f'<article class="file" id="{anchor(name, path)}">',
                f'<div class="file-head"><h3>{escape(label)}</h3><a href="{escape(url, quote=True)}">Open exact raw file ↗</a></div>',
                f'<pre>{escape(content, quote=False)}</pre>',
                '</article>',
            ))
        parts.append('</section>')
        sections.append("\n".join(parts))
    html = '''<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>Read the Simple Hack skills</title>
<meta name="description" content="Read the complete current Simple Hack skills and their original source files.">
<style>
:root{color-scheme:light dark;--bg:#f5f7f8;--surface:#fff;--text:#182027;--muted:#53616a;--border:#dce3e7;--accent:#066b83;--code:#edf1f3}
@media(prefers-color-scheme:dark){:root{--bg:#10171c;--surface:#182128;--text:#edf3f6;--muted:#adbfc8;--border:#31414a;--accent:#56c8de;--code:#24313a}}
*{box-sizing:border-box}html{scroll-behavior:smooth}body{margin:0;background:var(--bg);color:var(--text);font:16px/1.55 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}a{color:var(--accent);text-underline-offset:3px}header{background:var(--surface);border-bottom:1px solid var(--border)}.wrap{max-width:960px;margin:0 auto;padding:0 24px}header .wrap{min-height:64px;display:flex;align-items:center;justify-content:space-between;gap:16px}.brand{font-weight:700;color:var(--text);text-decoration:none}main{padding:42px 24px 80px}h1{font-size:clamp(30px,5vw,46px);letter-spacing:-.03em;line-height:1.15;margin:0 0 12px}h2{font-size:26px;letter-spacing:-.02em;margin:0 0 10px}h3{font-size:17px;margin:0}.eyebrow{color:var(--accent);font-size:13px;font-weight:700;text-transform:uppercase;letter-spacing:.08em;margin:0 0 12px}.lede{max-width:780px;color:var(--muted);font-size:18px;margin:0 0 22px}.roles{display:flex;flex-wrap:wrap;gap:8px;margin:24px 0 30px}.role{display:inline-flex;align-items:center;padding:9px 12px;border:1px solid var(--border);border-radius:8px;background:var(--surface);text-decoration:none;font-size:14px;font-weight:650}.skill{border-top:1px solid var(--border);padding-top:32px;margin-top:44px;scroll-margin-top:24px}.files{display:flex;flex-wrap:wrap;gap:5px 9px;color:var(--muted);font-size:14px;margin-bottom:22px}.file{border:1px solid var(--border);border-radius:10px;background:var(--surface);margin:18px 0;overflow:hidden;scroll-margin-top:20px}.file-head{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:14px 18px;border-bottom:1px solid var(--border)}.file-head a{font-size:14px;white-space:nowrap}pre{white-space:pre-wrap;overflow-wrap:anywhere;font:13px/1.6 ui-monospace,SFMono-Regular,Consolas,monospace;margin:0;padding:18px;background:var(--code);tab-size:2}.note{color:var(--muted);font-size:14px}.top{display:inline-block;margin-top:12px;font-size:14px}footer{border-top:1px solid var(--border);padding:24px;color:var(--muted);font-size:14px}@media(max-width:620px){.wrap{padding:0 16px}main{padding:30px 16px 60px}.file-head{align-items:flex-start;flex-direction:column;gap:4px}pre{font-size:12px;padding:14px}.roles{display:grid;grid-template-columns:1fr 1fr}.role{min-height:44px}}
</style>
</head>
<body id="top"><header><div class="wrap"><a class="brand" href="/">← Simple Hack toolkit</a><a href="https://simple-hack.app/">Simple Hack</a></div></header>
<main class="wrap"><p class="eyebrow">Complete current text · package 0.2.8 · skill version 0.27.20</p><h1>Read the Simple Hack skills</h1>
<p class="lede">These are the complete reviewed skill files, references and MCP dependency declarations in the current download. Each raw link opens the original file exactly as packaged.</p>
<p class="note">The five skills require the signed-in Simple Hack connector. No upload or sign-in happens on this page.</p>
<nav class="roles" aria-label="Jump to a skill">'''+"\n".join(nav)+'''</nav>
'''+"\n".join(sections)+'''
<a class="top" href="#top">Back to top ↑</a></main><footer><div class="wrap">Source: the current reviewed Simple Hack 0.2.8 package. <a href="/downloads/simple-hack-skills-only-0.2.8.zip">Download the ZIP</a>.</div></footer></body></html>
'''
    (DEST / "index.html").write_text(html, encoding="utf-8")
    print(f"Generated skills/index.html and {sum(len(files_for(name)) for name, _ in NAMES)} raw skill files")


if __name__ == "__main__":
    build()
