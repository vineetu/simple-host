/* Simple Hack prototype shell.
   A screen file holds only its <main class="page"> content and says who it is for:
     <body data-role="participant" data-nav="team" data-stage="building" data-next="Entries close in 2 days · Sun 18:00">
   An empty data-role="" (landing-your-events, the person's Home) means no event is open: the
   sidebar shows only its always-present top section (brand + Home + Find a hackathon), no
   event switcher, no role nav. Set data-top="home" on that body to mark Home current there.
   This script wraps it in the app: brand link, top nav, event switcher + role sidebar,
   status header, phone top bar and bottom tabs. The sidebar items come from nav.js
   (window.PROTO), which is generated from spec.json, so the prototype and the spec never
   disagree. A <template id="status"> in the page replaces the default status header.
   Desktop sidebar collapse (atlas 2026-10-30): the panel button beside the brand (or the
   [ key outside a text field) folds the sidebar into a 56px icon rail; the choice is kept
   per browser in localStorage 'sh_side'. data-side="collapsed" on <body> forces the rail
   for a prototype state. Phone is unchanged. */
(function(){
  var P = window.PROTO || {events:[], nav:{}};
  var b = document.body, role = b.dataset.role, here = b.dataset.nav, topCur = b.dataset.top;
  var hasEvent = !!role && !!P.nav[role];
  // Owner decision 2026-10-30: the one signed-in person this prototype demos as, across
  // every sample event and role.
  var PERSON = 'Amara';

  var I = {
    home:'M3 10.5 10 4l7 6.5V17a1 1 0 0 1-1 1h-3.5v-5h-5v5H4a1 1 0 0 1-1-1z',
    calendar:'M4 5h12a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zm-1 4h14M7 3v4m6-4v4',
    megaphone:'M3 8v4h3l6 4V4L6 8zm12-1.5a4 4 0 0 1 0 7',
    users:'M7.5 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM2 17c0-3 2.5-5 5.5-5s5.5 2 5.5 5m1-8a2.5 2.5 0 1 0 0-5m1.5 8c2 .4 3.5 2.2 3.5 5',
    // Rail rule (2026-10-30): no two sidebar icons may look alike, since the collapsed rail has
    // only icons. Teams is three heads (People is two), Overview is a dashboard (Home is the
    // house), Account is a person in a circle (Teams used to share its glyph).
    team:'M10 8a2.4 2.4 0 1 0 0-4.8A2.4 2.4 0 0 0 10 8zM4.6 10a1.9 1.9 0 1 0 0-3.8 1.9 1.9 0 0 0 0 3.8zM15.4 10a1.9 1.9 0 1 0 0-3.8 1.9 1.9 0 0 0 0 3.8zM6.2 16.5c0-2.4 1.7-4.3 3.8-4.3s3.8 1.9 3.8 4.3M1.8 15c0-1.7 1.2-2.9 2.8-2.9M18.2 15c0-1.7-1.2-2.9-2.8-2.9',
    overview:'M3 3h6v7H3zM11 3h6v4h-6zM11 9h6v8h-6zM3 12h6v5H3z',
    file:'M5 2h7l4 4v11a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1zm7 0v4h4M7 10h6M7 13h6',
    globe:'M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM2 10h16M10 2c2.2 2.2 3.3 4.9 3.3 8s-1.1 5.8-3.3 8c-2.2-2.2-3.3-4.9-3.3-8S7.8 4.2 10 2z',
    trophy:'M6 3h8v4a4 4 0 0 1-8 0zm0 1H3v1.5A2.5 2.5 0 0 0 5.5 8M14 4h3v1.5A2.5 2.5 0 0 1 14.5 8M10 11v3m-3 3h6m-5-3h4',
    queue:'M4 5h12M4 10h12M4 15h7',
    star:'M10 2.8l2.2 4.6 5 .7-3.6 3.5.9 5-4.5-2.4-4.5 2.4.9-5L2.8 8.1l5-.7z',
    rubric:'M5 3h10a1 1 0 0 1 1 1v13H4V4a1 1 0 0 1 1-1zm2 4h6M7 10h6M7 13h3',
    settings:'M10 12.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zm6.5-2.5-.1-1.2 1.6-1.3-1.6-2.8-2 .6a6 6 0 0 0-2-1.2L12 2H8l-.4 2.1a6 6 0 0 0-2 1.2l-2-.6L2 7.5l1.6 1.3a6 6 0 0 0 0 2.4L2 12.5l1.6 2.8 2-.6a6 6 0 0 0 2 1.2L8 18h4l.4-2.1a6 6 0 0 0 2-1.2l2 .6 1.6-2.8-1.6-1.3z',
    page:'M3 4h14v12H3zM3 8h14M6 6h.01',
    entries:'M4 4h5v5H4zM11 4h5v5h-5zM4 11h5v5H4zM11 11h5v5h-5z',
    chart:'M4 16V9m4 7V5m4 11v-5m4 5V7',
    vote:'M4 10l4 4 8-8',
    people:'M10 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zm-6 8c0-3.3 2.7-5.5 6-5.5s6 2.2 6 5.5',
    more:'M5 10h.01M10 10h.01M15 10h.01',
    user:'M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM10 10.2a2.6 2.6 0 1 0 0-5.2 2.6 2.6 0 0 0 0 5.2zM5.3 15.6c.9-1.9 2.6-3 4.7-3s3.8 1.1 4.7 3',
    signout:'M8 4H4v12h4M12 6l4 4-4 4M16 10H8',
    help:'M10 18a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM8 7.5a2 2 0 1 1 2.8 1.8c-.5.3-.8.7-.8 1.2V11m0 3h.01',
    events:'M3 5h14M3 10h14M3 15h14',
    collapse:'M3 4h14v12H3zM8 4v12M14 8l-2 2 2 2',
    expand:'M3 4h14v12H3zM8 4v12M12 8l2 2-2 2'
  };
  function ico(n,cls){ return '<svg class="'+(cls||'ico')+'" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="'+(I[n]||I.file)+'"/></svg>'; }
  function esc(s){ return String(s==null?'':s).replace(/[&<>"]/g,function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c];}); }
  function href(item){ return item.href || (role + '-' + item.id + '.html'); }

  // The brand link: top-left, always present, always leads Home. Owner decision
  // 2026-10-30 -- replaces the switcher's old top-left spot; the switcher now heads
  // the event section below instead.
  function brand(){
    return '<a class="brand" href="events.html" data-tip="Home · '+esc(PERSON)+'"><span class="mark"><span class="mark-full">simple<b>·</b>hack</span><span class="mark-short" aria-hidden="true">s<b>·</b>h</span><span class="sr-only mark-sr"> Home</span></span><span class="person">'+esc(PERSON)+'</span></a>';
  }
  // Remembered per browser; a prototype state can force it with data-side="collapsed".
  var collapsed = b.dataset.side === 'collapsed';
  if (!b.dataset.side) { try { collapsed = localStorage.getItem('sh_side') === 'collapsed'; } catch (e) {} }
  function toggleBtn(){
    return '<button class="side-toggle" type="button" data-side-toggle aria-controls="side" aria-expanded="'+(!collapsed)+'" aria-label="'+(collapsed?'Expand sidebar':'Collapse sidebar')+'" data-tip="'+(collapsed?'Expand sidebar  [':'Collapse sidebar  [')+'" title="'+(collapsed?'':'Collapse sidebar  [')+'">'+ico(collapsed?'expand':'collapse')+'</button>';
  }
  var topNav = '<nav class="top-nav"><a href="events.html" data-tip="Home"'+(topCur==='home'?' aria-current="page"':'')+'>'+ico('home')+'<span>Home</span></a>'+
    '<a href="https://simple-hack.app/directory" data-tip="Find a hackathon">'+ico('globe')+'<span>Find a hackathon</span></a></nav>';

  var eventSection = '', tabbar = '', phoneTop = brand();
  if (hasEvent) {
    // Every prototype screen with an event shows the sample event; the switcher
    // lists the person's other events too.
    var HOME_EV = 'Generation Girl Online Hack 2026';
    var home = P.events.filter(function(e){ return e.name === HOME_EV; })[0] || {name:HOME_EV, short:'GG', color:'#d15d48'};
    var STAGE = {'draft':'draft','not open yet':'draft','open':'open','sign-up open':'open','building':'building','entries closed':'closed','closed':'closed','judging':'judging','results':'results','ended':'ended'};
    var stageNow = STAGE[(b.dataset.stage||'').toLowerCase()] || '';
    var ev = {name:home.name, short:home.short, color:home.color, role:role, stage:b.dataset.stage || home.stage};
    P.events = [ev].concat(P.events.filter(function(e){ return e.name !== HOME_EV; }));
    var roleLabel = {organiser:'Organiser', participant:'Participant', judge:'Judge'}[role] || role;
    var items = P.nav[role];

    var switcherHTML = function(id){
      return '<button class="switcher" id="'+id+'" aria-expanded="false" aria-haspopup="true" data-tip="'+esc(ev.name)+' · '+esc(roleLabel)+'">'+
        '<span class="ev-mark" style="--ev:'+esc(ev.color||'#d15d48')+'">'+esc(ev.short)+'</span>'+
        '<span style="min-width:0"><span class="ev-name">'+esc(ev.name)+'</span><span class="ev-role">'+esc(roleLabel)+'</span></span>'+
        '<svg class="chev" width="14" height="14" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M6 8l4 4 4-4"/></svg></button>';
    };
    // Owner decision 2026-10-30: the dropdown only lists events now -- Home and Find
    // a hackathon already live in the sidebar's always-shown top section, so the old
    // "More" block here just repeated them.
    var pop = '<div class="menu-pop" id="evpop" role="menu"><div class="lbl">Your events</div>'+
      P.events.map(function(e){
        var r = {organiser:'Organiser', participant:'Participant', judge:'Judge'}[e.role];
        return '<a href="'+esc(e.role)+'-home.html"><span class="ev-mark" style="--ev:'+esc(e.color)+'">'+esc(e.short)+'</span><span>'+esc(e.name)+'<small>'+esc(r)+' · '+esc(e.stage)+'</small></span></a>';
      }).join('')+'</div>';

    items = items.map(function(it){
      if (it.stages && stageNow && it.stages.indexOf(stageNow) < 0) return Object.assign({}, it, {later:true, badge:null});
      return it;
    });
    var nav = items.map(function(it){
      if (it.sep) return '<div class="sep"></div>';
      var cur = it.id === here ? ' aria-current="page"' : '';
      var cnt = it.badge ? '<span class="count'+(it.badge_quiet?' quiet':'')+'">'+esc(it.badge)+'</span>' : '';
      var tip = it.label + (it.badge ? ' · ' + it.badge : '');
      return '<a href="'+esc(href(it))+'"'+cur+(it.later?' class="later"':'')+' data-tip="'+esc(tip)+'"'+(it.hint?' title="'+esc(it.hint)+'"':'')+'>'+ico(it.icon)+'<span>'+esc(it.label)+'</span>'+cnt+'</a>';
    }).join('');

    var tabs = items.filter(function(it){ return it.phone_tab; });
    var hasMore = items.some(function(it){ return !it.sep && !it.phone_tab; });
    tabbar = (role === 'organiser') ? '' :
      '<nav class="tabbar" aria-label="Sections">'+tabs.map(function(it){
        var cur = it.id === here ? ' aria-current="page"' : '';
        return '<a href="'+esc(href(it))+'"'+cur+'>'+ico(it.icon)+'<span>'+esc(it.short||it.label)+'</span>'+(it.badge&&!it.badge_quiet?'<span class="count">'+esc(it.badge)+'</span>':'')+'</a>';
      }).join('')+(hasMore?'<a href="#" data-more>'+ico('more')+'<span>More</span></a>':'')+'</nav>';

    eventSection = '<div class="event-nav">'+switcherHTML('sw1')+pop+'<nav class="nav">'+nav+'</nav></div>';
    // On phone, inside an event the top bar shows the switcher (context + quick
    // jump) instead of the brand -- the brand is still one tap away via Menu.
    phoneTop = switcherHTML('sw2');
  }

  var stage = b.dataset.stage, next = b.dataset.next;
  var tpl = document.getElementById('status');
  var status = tpl ? tpl.innerHTML :
    (stage ? '<span class="pill stage"><span class="d"></span>'+esc(stage)+'</span>' : '')+
    (next ? '<span class="muted">'+esc(next)+'</span>' : '')+'<span class="grow"></span>';

  var main = document.querySelector('main') || document.createElement('main');
  var bare = b.dataset.bare === '1';
  var app = document.createElement('div');
  app.className = 'app' + (tabbar ? ' has-tabs' : '') + (collapsed ? ' side-collapsed' : '');
  app.innerHTML =
    '<aside class="side" id="side" aria-label="Navigation"><div class="side-top">'+brand()+toggleBtn()+'</div>'+topNav+eventSection+
      '<div class="side-foot"><a href="account.html" data-tip="Account">'+ico('user')+'<span>Account</span></a><a href="#" data-tip="Sign out">'+ico('signout')+'<span>Sign out</span></a><a href="#" data-tip="Help">'+ico('help')+'<span>Help</span></a></div></aside>'+
    '<div class="col"><div class="phone-top">'+phoneTop+'<button class="menu-btn" data-menu>Menu</button></div>'+
      (bare ? '' : '<div class="status">'+status+'</div>')+
      '<div class="content"></div></div>'+tabbar;
  app.querySelector('.content').appendChild(main);
  b.insertBefore(app, b.firstChild);

  function toggle(btn){
    var p = document.getElementById('evpop'); var open = !p.classList.contains('open');
    // In the rail the event list opens beside the switcher instead of under it.
    if (open && btn.id === 'sw1') p.style.top = (app.classList.contains('side-collapsed') ? btn.getBoundingClientRect().top : btn.getBoundingClientRect().bottom + 4) + 'px';
    p.classList.toggle('open', open); btn.setAttribute('aria-expanded', open);
  }
  function setSide(c){
    collapsed = c; app.classList.toggle('side-collapsed', c);
    var t = app.querySelector('[data-side-toggle]'); t.outerHTML = toggleBtn();
    try { localStorage.setItem('sh_side', c ? 'collapsed' : 'expanded'); } catch (e) {}
    var n = app.querySelector('[data-side-toggle]'); if (n) n.focus();
  }
  app.addEventListener('click', function(e){ if (e.target.closest('[data-side-toggle]')) { e.preventDefault(); setSide(!collapsed); } });
  document.addEventListener('keydown', function(e){
    if (e.key !== '[' || e.metaKey || e.ctrlKey || e.altKey) return;
    var t = e.target; if (t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return;
    if (window.matchMedia('(max-width:760px)').matches) return;
    setSide(!collapsed);
  });
  ['sw1','sw2'].forEach(function(id){ var s = document.getElementById(id); if (s) s.onclick = function(e){ e.stopPropagation(); toggle(s); }; });
  document.addEventListener('click', function(e){
    var p = document.getElementById('evpop'); if (p && !p.contains(e.target)) p.classList.remove('open');
    var m = e.target.closest('[data-menu],[data-more]');
    if (m){ e.preventDefault(); app.classList.toggle('menu-open'); return; }
    if (app.classList.contains('menu-open') && !e.target.closest('.side')) app.classList.remove('menu-open');
  });
  b.classList.remove('booting');
})();
