#!/usr/bin/env bash
set -a; . /root/.vercel-dns.env; set +a
D=agent-deploy.dev; api=https://api.vercel.com
del(){ curl -s -X DELETE "$api/v2/domains/$D/records/$1?teamId=${TEAM}" -H "Authorization: Bearer $VTOKEN" >/dev/null && echo "deleted $2 ($1)"; }
# 1) dead records pointing at the deleted test box
del rec_e2de6a2d961a1e70a62c1762 "*.d"
del rec_ec700081e5b696f95282c150 "d"
del rec_ee351b0ee6b0130555946c21 "brand"
del rec_77eec7ded01572522afd5924 "dash"
del rec_c2ea60d914be623726def284 "sites"
# 2) apex ALIAS (Vercel) -> replace with A to our box
del rec_ab4d5afe8c271c3cca4733be "apex ALIAS"
# 3) apex A -> our prod box
curl -s -X POST "$api/v4/domains/$D/records?teamId=${TEAM}" \
  -H "Authorization: Bearer $VTOKEN" -H "Content-Type: application/json" \
  -d '{"type":"A","name":"","value":"209.151.146.185","ttl":60}' >/dev/null && echo "created apex A -> 209.151.146.185"
echo "=== records now ==="
curl -s "$api/v4/domains/$D/records?teamId=${TEAM}&limit=100" -H "Authorization: Bearer $VTOKEN" \
  | python3 -c "import sys,json;[print(r['type'],repr(r['name']),r.get('value')) for r in json.load(sys.stdin).get('records',[])]"
