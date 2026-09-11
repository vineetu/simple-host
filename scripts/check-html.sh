#!/usr/bin/env bash
# Every served page must nest correctly. A stray </div> is invisible in a diff
# and invisible in review: the browser recovers, the page still renders, and the
# only symptom is content landing in the wrong grid column. That shipped once —
# a paragraph escaped its content <div> on the hackathons page and rendered one
# word per line inside a 54px number column. This gate exists so it cannot
# happen twice.
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - "$@" <<'PY'
from html.parser import HTMLParser
import glob, sys

# Elements with no end tag. SVG shapes are here too: these pages inline SVG
# icons, and the HTML parser does not know they self-close.
VOID = {"meta", "link", "br", "img", "input", "hr", "source", "col", "area",
        "base", "wbr", "embed", "track", "param", "circle", "path", "rect",
        "line", "polyline", "polygon", "ellipse", "use", "stop"}

class Checker(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.stack, self.problems = [], []

    def handle_starttag(self, tag, attrs):
        if tag not in VOID:
            self.stack.append((tag, self.getpos()[0]))

    def handle_endtag(self, tag):
        if tag in VOID:
            return
        if not self.stack:
            self.problems.append("line %d: </%s> with nothing open" % (self.getpos()[0], tag))
            return
        top, line = self.stack[-1]
        if top == tag:
            self.stack.pop()
            return
        self.problems.append(
            "line %d: </%s> closes out of order; <%s> from line %d is still open"
            % (self.getpos()[0], tag, top, line))
        for i in range(len(self.stack) - 1, -1, -1):   # resync, then keep reading
            if self.stack[i][0] == tag:
                del self.stack[i:]
                return
        self.problems.append("line %d: </%s> matches no open tag" % (self.getpos()[0], tag))

failed = False
for path in sorted(glob.glob("internal/handler/static/*.html")):
    checker = Checker()
    checker.feed(open(path, encoding="utf-8").read())
    problems = checker.problems + [
        "<%s> opened on line %d is never closed" % (tag, line) for tag, line in checker.stack]
    if problems:
        failed = True
        print("FAIL %s" % path)
        for problem in problems:
            print("       %s" % problem)
    else:
        print("  ok   %s" % path)

if failed:
    print("\nhtml structure check failed")
    sys.exit(1)
print("\nhtml structure ok")
PY
