// The Simple Host Enterprise cost model: from a few numbers about an
// installation (people, sites, page views, sizes) to each provider's monthly
// bill, by line item. Every price and sizing constant comes from prices.json;
// this file only does the arithmetic, so the page, the setup helper and the
// tests (internal/handler/costs_test.go runs it under node) agree.
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.SHCosts = factory();
})(this, function () {
  'use strict';

  var CATEGORIES = [
    ['cluster', 'Cluster and nodes'],
    ['database', 'Database'],
    ['storage', 'Storage'],
    ['traffic', 'Traffic'],
    ['lb', 'Load balancer'],
    ['other', 'Other']
  ];

  function round2(n) { return Math.round(n * 100) / 100; }

  // monthly turns one price into dollars a month: hourly prices run all month.
  function monthly(price, hours) {
    if (!price) return 0;
    if (price.per === 'hour') return price.usd * hours;
    if (price.per === 'month') return price.usd;
    throw new Error('not a monthly or hourly price: ' + price.per);
  }

  // tiered prices a quantity (GB) against [[upTo, perGB], ...] after freeGB;
  // upTo counts from zero (the provider's own tier bounds), null is no bound.
  function tiered(gb, freeGB, tiers) {
    var cost = 0, from = freeGB || 0;
    for (var i = 0; i < tiers.length && gb > from; i++) {
      var upTo = tiers[i][0] === null ? Infinity : tiers[i][0];
      if (upTo <= from) continue;
      cost += (Math.min(gb, upTo) - from) * tiers[i][1];
      from = upTo;
    }
    return cost;
  }

  // clean reads the inputs, falling back to the defaults for anything missing
  // or not a number, and never below zero (people at least one). A traffic
  // level other than custom sets page views and data per page view from the
  // number of people.
  function clean(model, inp) {
    var d = model.defaults, out = {};
    inp = inp || {};
    ['people', 'sites', 'views', 'siteMB', 'savedMB', 'uploadsGB', 'viewMB'].forEach(function (k) {
      var v = inp[k] !== undefined && inp[k] !== '' && inp[k] !== null ? Number(inp[k]) : d[k];
      out[k] = isFinite(v) && v >= 0 ? v : d[k];
    });
    out.people = Math.max(1, Math.round(out.people));
    out.sites = Math.round(out.sites);
    ['ha', 'existing', 'ingress'].forEach(function (k) {
      out[k] = inp[k] !== undefined ? !!inp[k] : !!d[k];
    });
    out.network = (inp.network || d.network) === 'private' ? 'private' : 'internet';
    out.traffic = inp.traffic || d.traffic;
    var level = trafficLevel(model, out.traffic);
    if (level) {
      out.views = out.people * level.pages_per_day * model.working_days_per_month;
      out.viewMB = level.mb_per_page;
    } else {
      out.traffic = 'custom';
    }
    out.views = Math.round(out.views);
    return out;
  }

  function trafficLevel(model, id) {
    for (var i = 0; i < model.traffic_levels.length; i++) if (model.traffic_levels[i].id === id) return model.traffic_levels[i];
    return null;
  }

  // sizing is what the installation needs, the same on every provider.
  function sizing(prices, inp) {
    var m = prices.model, x = clean(m, inp);
    var replicas = Math.max(m.replicas_min, Math.ceil(x.views / m.page_views_per_replica));
    var memGiB = m.system_mem_gib + replicas * m.replica_mem_gib + m.upload_headroom_gib;
    var cpu = m.system_cpu + replicas * m.replica_cpu;
    var dbTier = 8;
    for (var i = 0; i < m.db_tiers_by_people.length; i++) {
      var t = m.db_tiers_by_people[i];
      if (t[0] === null || x.people <= t[0]) { dbTier = t[1]; break; }
    }
    if (replicas >= m.db_min_tier_from_replicas && dbTier < 2) dbTier = 2;
    var dbGB = m.db_base_gb + x.sites * x.savedMB * m.saved_data_factor / 1024 +
      x.views * m.access_log_months * m.access_log_kb_per_view / (1024 * 1024) +
      x.people * m.db_mb_per_person / 1024;
    var bucketGB = x.sites * x.siteMB * m.versions_kept * m.replaced_versions_factor / 1024 + x.uploadsGB;
    var egressGB = x.views * x.viewMB / 1024;
    // On a cluster that already runs, Simple Host adds only its own pods:
    // their requests, plus room for one upload at the memory limit.
    var shareMemGiB = replicas * m.replica_mem_gib + m.upload_headroom_gib;
    var shareCPU = replicas * m.replica_cpu;
    return { inputs: x, replicas: replicas, memGiB: memGiB, cpu: cpu, shareMemGiB: shareMemGiB, shareCPU: shareCPU, dbTier: dbTier, dbGB: dbGB, bucketGB: bucketGB, egressGB: egressGB };
  }

  // pickNodes is the cheapest (type, count) with at least min nodes that fits
  // the memory and CPU, each node keeping node_reserved_gib for itself.
  function pickNodes(p, need, min, reserved, hours) {
    var best = null;
    p.nodes.forEach(function (n) {
      for (var count = min; count <= 50; count++) {
        if (count * (n.mem - reserved) >= need.memGiB && count * n.cpu >= need.cpu) {
          var cost = count * monthly(n.price, hours);
          if (!best || cost < best.cost - 1e-9) best = { node: n, count: count, cost: cost };
          break;
        }
      }
    });
    return best;
  }

  function pickDBTier(p, s) {
    var tiers = p.db.tiers, i;
    for (i = 0; i < tiers.length; i++) if (tiers[i].mem >= s.dbTier) break;
    if (i === tiers.length) i = tiers.length - 1;
    if (p.db.storage.by_tier_disk) {
      while (i < tiers.length - 1 && tiers[i].disk_gb < s.dbGB) i++;
    }
    return tiers[i];
  }

  function gb(n) { return n < 10 ? n.toFixed(1) : String(Math.round(n)); }
  function money(n) { return '$' + n.toFixed(2); }
  // unit shows a unit price with the digits it has: $0.0336, $0.10, $19.71.
  function unit(n) {
    if (n >= 1 || n === 0) return money(n);
    var s = n.toFixed(5).replace(/0+$/, '');
    return '$' + (s.split('.')[1].length < 2 ? n.toFixed(2) : s);
  }

  // estimate is one provider's monthly bill for the inputs: the total, per
  // person, per category, and the line items to enter in its calculator.
  function estimate(prices, providerId, inp) {
    var p = null;
    prices.providers.forEach(function (q) { if (q.id === providerId) p = q; });
    if (!p) throw new Error('unknown provider ' + providerId);
    var m = prices.model, H = m.hours_per_month, s = sizing(prices, inp), ha = s.inputs.ha;
    var items = [];
    function add(cat, what, detail, usd) { items.push({ cat: cat, what: what, detail: detail, usd: round2(usd) }); }

    var existing = s.inputs.existing, sharedLB = existing && s.inputs.ingress;
    var nodes;
    if (existing) {
      // Your cluster: no control plane, and only the share of a node the pods
      // take, priced at whichever of the provider's node sizes is cheapest
      // for it.
      var best = null;
      p.nodes.forEach(function (n) {
        var frac = Math.max(s.shareMemGiB / n.mem, s.shareCPU / n.cpu);
        var cost = frac * monthly(n.price, H);
        if (!best || cost < best.cost - 1e-9) best = { node: n, frac: frac, cost: cost };
      });
      nodes = { node: best.node, count: best.frac, share: true };
      add('cluster', 'Node capacity on your cluster: ' + gb(s.shareMemGiB) + ' GiB, ' + s.shareCPU.toFixed(2).replace(/0$/, '') + ' CPU',
        best.frac.toFixed(2) + ' of a ' + best.node.name + ' (' + (best.node.price.per === 'hour' ? unit(best.node.price.usd) + ' an hour' : unit(best.node.price.usd) + ' a month') + ')', best.cost);
    } else {
      // A new cluster: control plane (with GKE's free tier credit), nodes.
      var cp = ha ? p.control_plane.ha : p.control_plane.base;
      var cpUSD = monthly(cp, H);
      add('cluster', p.control_plane.label + (cp.name ? ', ' + cp.name : ''), cp.usd ? (cp.per === 'hour' ? unit(cp.usd) + ' an hour' : unit(cp.usd) + ' a month') : 'free', cpUSD);
      var credit = p.control_plane.credit;
      if (credit && credit.applies === (ha ? 'ha' : 'base') && cpUSD > 0) {
        add('cluster', credit.label, 'up to ' + money(credit.usd) + ' a month', -Math.min(cpUSD, credit.usd));
      }
      nodes = pickNodes(p, s, ha ? p.min_nodes.ha : p.min_nodes.base, m.node_reserved_gib, H);
      add('cluster', nodes.count + ' × ' + nodes.node.name + ' node' + (nodes.count > 1 ? 's' : ''),
        nodes.node.cpu + ' CPU, ' + nodes.node.mem + ' GB each, ' + (nodes.node.price.per === 'hour' ? unit(nodes.node.price.usd) + ' an hour' : unit(nodes.node.price.usd) + ' a month'), nodes.cost);
      if (p.node_disk) {
        var disk = p.node_disk.each ? monthly(p.node_disk.each, H) : p.node_disk.gb * p.node_disk.price.usd;
        add('other', nodes.count + ' × ' + p.node_disk.label, money(disk) + ' a month each', nodes.count * disk);
      }
    }

    // Database: the tier, then storage past what the tier includes.
    var tier = pickDBTier(p, s);
    var dbPrice = ha ? tier.ha_price : tier.price;
    var dbUSD = monthly(dbPrice, H);
    add('database', p.db.label + ', ' + (ha ? tier.ha_name : tier.name), dbPrice.per === 'hour' ? unit(dbPrice.usd) + ' an hour' : unit(dbPrice.usd) + ' a month', dbUSD);
    if (p.db.backup_pct) add('database', 'Server backups (' + p.db.backup_pct + '% of the server price)', '7 daily copies', dbUSD * p.db.backup_pct / 100);
    var st = p.db.storage;
    if (!st.by_tier_disk) {
      var provisioned = Math.max(st.min_gb, Math.ceil(s.dbGB));
      var billable = Math.max(0, provisioned - st.included_gb);
      var sp = ha ? st.ha_price : st.price;
      if (billable > 0 || st.min_gb > 0) add('database', 'Database ' + st.label + ', ' + billable + ' GB', unit(sp.usd) + ' per GB a month', billable * sp.usd);
    }

    // Storage: the bucket.
    var b = p.bucket, bUSD, bDetail;
    if (b.block_gb) {
      var blocks = Math.max(1, Math.ceil(s.bucketGB / b.block_gb));
      bUSD = blocks * b.price.usd;
      bDetail = blocks * b.block_gb + ' GB (' + money(b.price.usd) + ' per ' + b.block_gb + ' GB)';
    } else if (b.base) {
      bUSD = b.base.usd + Math.max(0, s.bucketGB - b.base.included_gb) * b.price.usd;
      bDetail = gb(s.bucketGB) + ' GB (' + money(b.base.usd) + ' a month includes ' + b.base.included_gb / 1024 + ' TB)';
    } else {
      bUSD = s.bucketGB * b.price.usd;
      bDetail = gb(s.bucketGB) + ' GB at ' + unit(b.price.usd) + ' per GB a month';
    }
    add('storage', b.label, bDetail, bUSD);

    // Traffic: page views leaving the cloud, over the internet or a private
    // link the company already has.
    var e = p.egress;
    if (s.inputs.network === 'private' && p.private_link) {
      add('traffic', p.private_link.label + ', ' + gb(s.egressGB) + ' GB', unit(p.private_link.price.usd) + ' per GB', s.egressGB * p.private_link.price.usd);
    } else {
      add('traffic', e.label + ', ' + gb(s.egressGB) + ' GB', e.free_note, tiered(s.egressGB, e.free_gb, e.tiers));
    }

    // Load balancer: a new one, or only the traffic it adds to yours.
    var lb = p.lb;
    if (sharedLB) {
      if (lb.lcu) {
        var used = s.egressGB / H / lb.lcu.gb_per_hour;
        add('lb', 'Your load balancer: capacity units this traffic adds, ' + used.toFixed(2) + ' on average', unit(lb.lcu.usd) + ' per unit-hour', used * lb.lcu.usd * H);
      }
      if (lb.gb) add('lb', 'Your load balancer: data it processes, ' + gb(s.egressGB) + ' GB', unit(lb.gb.usd) + ' per GB', s.egressGB * lb.gb.usd);
    } else if (lb.hour) {
      add('lb', lb.label, unit(lb.hour.usd) + ' an hour', monthly(lb.hour, H));
      if (lb.lcu) {
        var lcus = Math.max(1, s.egressGB / H / lb.lcu.gb_per_hour);
        add('lb', 'Load balancer capacity units, ' + lcus.toFixed(lcus < 10 ? 2 : 0) + ' on average', unit(lb.lcu.usd) + ' per unit-hour', lcus * lb.lcu.usd * H);
      }
      if (lb.gb) add('lb', 'Data processed by the load balancer, ' + gb(s.egressGB) + ' GB', unit(lb.gb.usd) + ' per GB', s.egressGB * lb.gb.usd);
    } else {
      var lm = ha && lb.ha_month ? lb.ha_month : lb.month;
      add('lb', lb.label + (lm.name ? ', ' + lm.name : ''), money(lm.usd) + ' a month', lm.usd);
    }
    // Azure bills public addresses apart: a new cluster needs one for the
    // ingress and one for outbound traffic; a new ingress on your cluster, one.
    var ipCount = !p.ips || sharedLB ? 0 : existing ? 1 : p.ips.count;
    if (ipCount) add('other', ipCount + ' × ' + p.ips.label.replace(/^Standard public/, 'standard public').replace(/ \(ingress and outbound\)$/, ''), unit(p.ips.price.usd) + ' an hour each', ipCount * monthly(p.ips.price, H));

    var cats = {}, total = 0;
    CATEGORIES.forEach(function (c) { cats[c[0]] = 0; });
    items.forEach(function (it) { cats[it.cat] += it.usd; total += it.usd; });
    Object.keys(cats).forEach(function (k) { cats[k] = round2(cats[k]); });
    total = round2(total);
    return {
      provider: p, sizing: s, items: items, cats: cats, total: total,
      perPerson: Math.round(total / s.inputs.people * 1000) / 1000,
      nodes: { name: nodes.node.name, count: nodes.count, share: !!nodes.share }, db: ha ? tier.ha_name : tier.name
    };
  }

  // headline is the range the enterprise pages quote for a number of people:
  // the lowest and highest monthly total across model.headline's providers
  // and traffic levels at the calculator's defaults, rounded out to
  // round_usd, and the same per person.
  function headline(prices, people) {
    var h = prices.model.headline, lo = Infinity, hi = 0;
    h.providers.forEach(function (id) {
      h.levels.forEach(function (lv) {
        var e = estimate(prices, id, { people: people, sites: people * h.sites_per_person, traffic: lv });
        lo = Math.min(lo, e.total); hi = Math.max(hi, e.total);
      });
    });
    var low = Math.floor(lo / h.round_usd) * h.round_usd, high = Math.ceil(hi / h.round_usd) * h.round_usd;
    return { people: people, low: low, high: high, perLow: low / people, perHigh: high / people, exactLow: lo, exactHigh: hi };
  }

  // checkedDates lists every "checked" date in the price file, for the
  // oldest-date line on the page.
  function checkedDates(obj, out) {
    out = out || [];
    if (obj && typeof obj === 'object') {
      Object.keys(obj).forEach(function (k) {
        if (k === 'checked' && typeof obj[k] === 'string') out.push(obj[k]);
        else checkedDates(obj[k], out);
      });
    }
    return out;
  }

  return { CATEGORIES: CATEGORIES, estimate: estimate, headline: headline, trafficLevel: trafficLevel, sizing: sizing, tiered: tiered, checkedDates: checkedDates };
});
