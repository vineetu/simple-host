// Admin > API > Growth: API calls, new accounts and new sites per day, and a
// world map of where API calls come from. Reads GET /v1/admin/growth.
//
// Everything is drawn here as inline SVG; the map is /world-map.svg (Natural
// Earth country shapes, public domain) served by this server. No script,
// tile or font is fetched from anywhere else, so no visitor data leaves.
// admin.html calls window.shGrowth.start(apiKey) the first time the API tab
// opens.
(function(){
'use strict';
var RANGES=[['7d','7 days'],['14d','14 days'],['30d','30 days'],['6m','6 months']];
var RANGE_WORD={'7d':'7 days','14d':'14 days','30d':'30 days','6m':'6 months'};
var GROUP_LABEL={deploy:'Deploy & sites',data:'Saved data',auth:'Sign-in & keys',connector:'Connector (MCP)',admin:'Admin',other:'Other'};
var MONTHS=['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
var SVGNS='http://www.w3.org/2000/svg';
var key='', range='30d', data=null, root=null, tip=null, mapPromise=null, seq=0;
try{ range=localStorage.getItem('adminGrowthRange')||'30d'; }catch(e){}
if(!RANGE_WORD[range]) range='30d';

function $(id){ return document.getElementById(id); }
function esc(s){ return String(s==null?'':s).replace(/[&<>"']/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];}); }
function num(n){ return Number(n||0).toLocaleString('en-US'); }
function short(n){ n=Number(n)||0; if(n>=1e6) return (n/1e6).toFixed(n>=1e7?0:1).replace(/\.0$/,'')+'M'; if(n>=1e4) return Math.round(n/1e3)+'k'; if(n>=1e3) return (n/1e3).toFixed(1).replace(/\.0$/,'')+'k'; return String(n); }
function day(s){ var p=s.split('-'); return {y:+p[0], m:+p[1]-1, d:+p[2]}; }
function dayLabel(s, withYear){ var x=day(s); return x.d+' '+MONTHS[x.m]+(withYear?' '+x.y:''); }
function el(tag, attrs){ var e=document.createElementNS(SVGNS, tag); for(var k in attrs) e.setAttribute(k, attrs[k]); return e; }
function niceMax(v){ if(v<=4) return 4; var p=Math.pow(10, Math.floor(Math.log10(v))), f=v/p; var n=f<=1?1:f<=2?2:f<=2.5?2.5:f<=5?5:10; return n*p; }

function build(){
  root=$('growth');
  var chips=RANGES.map(function(r){ return '<button type="button" class="chip" data-range="'+r[0]+'" aria-pressed="'+(r[0]===range)+'">'+r[1]+'</button>'; }).join('');
  root.innerHTML=
    '<h2>Growth</h2>'+
    '<div class="g-top"><div class="chips" role="group" aria-label="Time range" id="g-range">'+chips+'</div></div>'+
    '<p class="ph" id="g-note"></p>'+
    '<div class="g-grid" id="g-body">'+
      card('api','API calls',true)+card('users','New accounts',false)+card('sites','New sites',false)+
    '</div>'+
    '<div class="sec"><h2>Where API calls come from</h2>'+
      '<p class="ph" id="g-geo-note"></p>'+
      '<div class="g-geo"><div class="g-map" id="g-map" role="img" aria-label="World map of API calls by country"></div>'+
      '<div><ol class="g-top10" id="g-top10"></ol></div></div>'+
      '<p class="geo-credit">Countries are looked up on this server from a local copy of <a href="https://db-ip.com">DB-IP</a>; no address is stored with these counts or sent anywhere.</p>'+
    '</div>';
  tip=document.createElement('div'); tip.className='g-tip'; tip.hidden=true; tip.setAttribute('role','status');
  document.body.appendChild(tip);
  $('g-range').addEventListener('click', function(e){
    var b=e.target.closest('[data-range]'); if(!b||b.dataset.range===range) return;
    range=b.dataset.range;
    try{ localStorage.setItem('adminGrowthRange', range); }catch(e2){}
    [].forEach.call(root.querySelectorAll('[data-range]'), function(c){ c.setAttribute('aria-pressed', String(c.dataset.range===range)); });
    load();
  });
  document.addEventListener('scroll', hideTip, {passive:true});
  document.addEventListener('pointerdown', function(e){ if(!e.target.closest('.g-chart,.g-map')) hideTip(); });
  var rt=null;
  window.addEventListener('resize', function(){ clearTimeout(rt); rt=setTimeout(function(){ if(data&&root.offsetParent) drawCharts(); }, 120); });
}
function card(id, title, wide){
  return '<div class="g-card'+(wide?' wide':'')+'" id="g-'+id+'"><h3>'+title+'</h3><div class="g-total" id="g-'+id+'-total">–</div>'+
    '<div class="g-chg" id="g-'+id+'-chg"></div><div class="g-chart" id="g-'+id+'-chart"></div><div class="g-cap" id="g-'+id+'-cap"></div>'+
    (wide?'<div class="g-legend" id="g-'+id+'-legend"></div>':'')+'</div>';
}

async function load(){
  var my=++seq;
  $('g-body').classList.add('g-busy');
  try{
    var res=await fetch('/v1/admin/growth?range='+encodeURIComponent(range), {headers:{'X-API-Key':key}});
    if(my!==seq) return;
    if(!res.ok){ $('g-note').textContent='Could not load growth (HTTP '+res.status+').'; return; }
    data=await res.json();
    if(my!==seq) return;
    render();
  }catch(e){ if(my===seq) $('g-note').textContent='Cannot reach the server.'; }
  finally{ if(my===seq) $('g-body').classList.remove('g-busy'); }
}

function changeText(s, noun){
  var w='previous '+RANGE_WORD[data.range];
  if(s.change_pct==null){
    if(noun==='calls' && data.tracking_since && data.tracking_since>prevFrom()) return 'Counted since '+dayLabel(data.tracking_since, true);
    return s.previous>0?'':(s.total>0?'None in the '+w:'');
  }
  var p=s.change_pct, sign=p>0?'+':p<0?'−':'±';
  return '<b>'+sign+Math.abs(p).toLocaleString('en-US')+'%</b> vs '+w+' ('+num(s.previous)+')';
}
function prevFrom(){ var d=new Date(data.from+'T00:00:00Z'); d.setUTCDate(d.getUTCDate()-data.days); return d.toISOString().slice(0,10); }

function render(){
  $('g-note').textContent=dayLabel(data.from, true)+' to '+dayLabel(data.to, true)+', UTC days.'+
    (data.self_calls?' API calls leave out '+num(data.self_calls)+' from this server (its own checks).':'');
  [['api','calls'],['users','accounts'],['sites','sites']].forEach(function(x){
    var s=data[x[0]];
    $('g-'+x[0]+'-total').textContent=num(s.total);
    $('g-'+x[0]+'-chg').innerHTML=changeText(s, x[1]);
  });
  var lg=data.group_order.map(function(g, i){
    var t=(data.api.groups[g]||[]).reduce(function(a,b){ return a+b; },0);
    return '<span><i style="background:var(--g-'+(i+1)+')"></i>'+esc(GROUP_LABEL[g]||g)+' <b>'+num(t)+'</b></span>';
  });
  $('g-api-legend').innerHTML=lg.join('');
  drawCharts();
  renderGeo();
}

// ------------------------------------------------------------------ charts
// Six months is shown per week (26 bars): a day would be under 2px wide.
function buckets(){
  var n=data.dates.length, size=data.range==='6m'?7:1, out=[];
  for(var i=0;i<n;i+=size) out.push({from:i, to:Math.min(n,i+size)-1});
  return {list:out, weekly:size>1};
}
function sum(arr, b){ var t=0; for(var i=b.from;i<=b.to;i++) t+=arr[i]||0; return t; }
function drawCharts(){
  var b=buckets();
  var groups=data.group_order.map(function(g,i){ return {key:g, color:'var(--g-'+(i+1)+')', vals:data.api.groups[g]||[]}; });
  bars('g-api-chart', b, groups, 'calls');
  bars('g-users-chart', b, [{key:'users', color:'var(--g-line)', vals:data.users.daily}], 'new accounts');
  bars('g-sites-chart', b, [{key:'sites', color:'var(--g-line)', vals:data.sites.daily}], 'new sites');
  var cap=b.weekly?'Per week (weeks start '+dayLabel(data.dates[0])+').':'Per day.';
  ['api','users','sites'].forEach(function(id){ $('g-'+id+'-cap').textContent=cap; });
}
function bars(id, bk, series, noun){
  var box=$(id), W=Math.max(240, Math.round(box.clientWidth||600)), H=id==='g-api-chart'?190:140;
  var padL=34, padR=4, padT=8, padB=22, iw=W-padL-padR, ih=H-padT-padB;
  var n=bk.list.length, slot=iw/n, gap=Math.min(2, slot*0.25), bw=Math.max(1, slot-gap);
  var totals=bk.list.map(function(b){ return series.reduce(function(a,s){ return a+sum(s.vals,b); },0); });
  var max=niceMax(Math.max.apply(null, totals.concat([0])));
  var svg=el('svg',{viewBox:'0 0 '+W+' '+H, width:W, height:H, role:'img'});
  var total=totals.reduce(function(a,b){ return a+b; },0);
  svg.setAttribute('aria-label', num(total)+' '+noun+' from '+dayLabel(data.from,true)+' to '+dayLabel(data.to,true)+'; highest '+(bk.weekly?'week':'day')+' '+num(Math.max.apply(null,totals)));
  [0, .5, 1].forEach(function(f){
    var y=padT+ih-ih*f;
    svg.appendChild(el('line',{x1:padL, x2:W-padR, y1:y, y2:y, 'class':f===0?'base':'grid'}));
    var t=el('text',{x:padL-6, y:y+3.5, 'text-anchor':'end', 'class':'ax'}); t.textContent=short(max*f); svg.appendChild(t);
  });
  var hl=el('rect',{x:0, y:padT, width:slot, height:ih, 'class':'hl', visibility:'hidden'}); svg.appendChild(hl);
  bk.list.forEach(function(b, i){
    var x=padL+i*slot+gap/2, y0=padT+ih, segs=[];
    series.forEach(function(s){ var v=sum(s.vals,b); if(v>0) segs.push({v:v, c:s.color}); });
    segs.forEach(function(sg, j){
      var h=sg.v/max*ih, top=j===segs.length-1;
      // 1px surface gap between stacked segments; rounded data-end on top only.
      var hh=Math.max(0.5, h-(j>0&&h>2?1:0));
      var y=y0-h;
      if(top && bw>=4 && hh>=3){
        var r=Math.min(3, bw/2, hh);
        svg.appendChild(el('path',{d:'M'+x+' '+(y+hh)+'V'+(y+r)+'Q'+x+' '+y+' '+(x+r)+' '+y+'H'+(x+bw-r)+'Q'+(x+bw)+' '+y+' '+(x+bw)+' '+(y+r)+'V'+(y+hh)+'Z', fill:sg.c}));
      } else {
        svg.appendChild(el('rect',{x:x, y:y, width:bw, height:hh, fill:sg.c}));
      }
      y0-=h;
    });
  });
  var lbl=[0, Math.floor((n-1)/2), n-1].filter(function(v,i,a){ return a.indexOf(v)===i; });
  lbl.forEach(function(i, k){
    var x=padL+i*slot+slot/2, anchor=k===0?'start':(i===n-1?'end':'middle');
    if(anchor==='start') x=padL; if(anchor==='end') x=W-padR;
    var t=el('text',{x:x, y:H-6, 'text-anchor':anchor, 'class':'ax'}); t.textContent=dayLabel(data.dates[bk.list[i].from]); svg.appendChild(t);
  });
  box.innerHTML=''; box.appendChild(svg);
  function at(e){
    var r=svg.getBoundingClientRect(), x=(e.clientX-r.left)*(W/r.width)-padL, i=Math.floor(x/slot);
    if(i<0||i>=n){ hl.setAttribute('visibility','hidden'); hideTip(); return; }
    hl.setAttribute('x', padL+i*slot); hl.setAttribute('visibility','visible');
    var b=bk.list[i], when=bk.weekly?('Week of '+dayLabel(data.dates[b.from], true)):dayLabel(data.dates[b.from], true);
    var rows=series.length>1?series.slice().reverse().map(function(s){ var v=sum(s.vals,b); return v?'<div class="r"><span><i style="background:'+s.color+'"></i>'+esc(GROUP_LABEL[s.key]||s.key)+'</span><span>'+num(v)+'</span></div>':''; }).join(''):'';
    showTip(e.clientX, e.clientY, '<div class="d">'+esc(when)+'</div><div class="r"><strong>'+num(totals[i])+' '+esc(noun)+'</strong></div>'+rows);
  }
  svg.addEventListener('pointermove', at);
  svg.addEventListener('pointerdown', at);
  svg.addEventListener('pointerleave', function(e){ if(e.pointerType==='mouse'){ hl.setAttribute('visibility','hidden'); hideTip(); } });
}

// ------------------------------------------------------------------ map
function loadMap(){
  if(!mapPromise) mapPromise=fetch('/world-map.svg').then(function(r){ if(!r.ok) throw new Error('HTTP '+r.status); return r.text(); })
    .then(function(txt){
      var doc=new DOMParser().parseFromString(txt, 'image/svg+xml'), s=doc.documentElement;
      if(!s||s.nodeName!=='svg') throw new Error('bad map');
      var svg=document.importNode(s, true);
      svg.setAttribute('aria-hidden','true');
      return svg;
    }).catch(function(e){ mapPromise=null; throw e; });
  return mapPromise;
}
function bin(v, max){ if(!v) return 0; if(max<=1) return 5; return Math.max(1, Math.min(5, Math.ceil(Math.log(1+v)/Math.log(1+max)*5))); }
function renderGeo(){
  var cs=data.countries||[], byCC={}, max=cs.length?cs[0].calls:0, total=cs.reduce(function(a,c){ return a+c.calls; },0);
  cs.forEach(function(c){ byCC[c.code]=c; });
  var top=cs.slice(0,10);
  $('g-top10').innerHTML=top.map(function(c,i){
    var share=total?Math.round(c.calls/total*1000)/10:0;
    return '<li><span class="rk">'+(i+1)+'</span><span class="nm">'+esc(c.name||c.code)+'</span><span class="ct">'+num(c.calls)+'<small>'+share+'%</small></span>'+
      '<span class="br"><div style="width:'+(max?Math.max(2,c.calls/max*100):0)+'%"></div></span></li>';
  }).join('')||'<li><span></span><span class="nm muted">No calls with a known country in this range.</span><span></span></li>';
  var note=cs.length+' '+(cs.length===1?'country':'countries')+', '+num(total)+' calls.';
  if(data.unknown_country_calls) note+=' '+num(data.unknown_country_calls)+' more came from an address with no known country.';
  $('g-geo-note').textContent=note;
  loadMap().then(function(svg){
    var box=$('g-map');
    if(svg.parentNode!==box){ box.innerHTML=''; box.appendChild(svg); wireMap(box); }
    [].forEach.call(svg.querySelectorAll('path[data-cc]'), function(p){
      var c=byCC[p.getAttribute('data-cc')];
      p.style.fill='var(--g-seq-'+bin(c?c.calls:0, max)+')';
    });
    var old=box.querySelector('.g-scale'); if(old) old.remove();
    if(max>0){
      var sc=document.createElement('div'); sc.className='g-scale';
      var sw='<span class="sw">'+[1,2,3,4,5].map(function(k){ return '<i style="background:var(--g-seq-'+k+')"></i>'; }).join('')+'</span>';
      sc.innerHTML='<span>'+num(1)+'</span>'+sw+'<span>'+num(max)+' calls</span><span class="sw" style="margin-left:10px"><i style="background:var(--g-seq-0)"></i></span><span>none</span>';
      box.appendChild(sc);
    }
    box.setAttribute('aria-label','World map of API calls by country. Top: '+top.slice(0,3).map(function(c){ return c.name+' '+num(c.calls); }).join(', '));
    box._byCC=byCC;
  }).catch(function(){ $('g-map').innerHTML='<p class="ph">The map could not be loaded.</p>'; });
}
function wireMap(box){
  var last=null;
  function at(e){
    var p=e.target.closest&&e.target.closest('path[data-cc]');
    if(last&&last!==p) last.classList.remove('on');
    if(!p){ hideTip(); last=null; return; }
    p.classList.add('on'); last=p;
    var c=(box._byCC||{})[p.getAttribute('data-cc')];
    showTip(e.clientX, e.clientY, '<div class="d">'+esc(c&&c.name||p.getAttribute('data-name'))+'</div><strong>'+(c?num(c.calls)+' call'+(c.calls===1?'':'s'):'No calls')+'</strong>');
  }
  box.addEventListener('pointermove', at);
  box.addEventListener('pointerdown', at);
  box.addEventListener('pointerleave', function(e){ if(e.pointerType==='mouse'){ if(last) last.classList.remove('on'); last=null; hideTip(); } });
}

// ------------------------------------------------------------------ tooltip
function showTip(x, y, html){
  tip.innerHTML=html; tip.hidden=false;
  var w=tip.offsetWidth, h=tip.offsetHeight, vw=document.documentElement.clientWidth, vh=window.innerHeight;
  var left=x+14, topY=y-h-12;
  if(left+w>vw-8) left=Math.max(8, x-w-14);
  if(topY<8) topY=Math.min(vh-h-8, y+16);
  tip.style.left=left+'px'; tip.style.top=topY+'px';
}
function hideTip(){ if(tip) tip.hidden=true; }

window.shGrowth={
  start:function(apiKey){ key=apiKey; if(!root) build(); load(); }
};
})();
