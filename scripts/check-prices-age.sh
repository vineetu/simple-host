#!/usr/bin/env bash
# The cost calculator's list prices go stale: every price in
# internal/handler/static/costs/prices.json carries the day it was checked.
# This fails (exit 1) when any is older than MAX_AGE_DAYS (45), naming each one,
# so a monthly look at the providers' pages is not forgotten. `make check`
# runs it as a warning only.
#
#   bash scripts/check-prices-age.sh            (MAX_AGE_DAYS=45 by default)
set -u
cd "$(dirname "$0")/.."
python3 - "${MAX_AGE_DAYS:-45}" <<'PY'
import datetime, json, sys
limit = int(sys.argv[1])
doc = json.load(open("internal/handler/static/costs/prices.json"))
today = datetime.date.today()
stale, seen = [], 0
def walk(path, v):
    global seen
    if isinstance(v, dict):
        for k, x in v.items():
            if k == "checked" and isinstance(x, str):
                seen += 1
                age = (today - datetime.date.fromisoformat(x)).days
                if age > limit:
                    stale.append("%s (%s, %d days)" % (path, x, age))
            else:
                walk(path + "." + k, x)
    elif isinstance(v, list):
        for i, x in enumerate(v):
            walk("%s[%s]" % (path, x.get("id", i) if isinstance(x, dict) else i), x)
walk("prices", doc)
if stale:
    print("prices older than %d days (%d of %d): check each source page and update prices.json:" % (limit, len(stale), seen))
    for s in stale[:40]:
        print("  " + s)
    sys.exit(1)
print("ok — all %d prices checked within %d days" % (seen, limit))
PY
