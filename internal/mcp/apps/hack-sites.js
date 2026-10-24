(function (root) {
  'use strict';
  // This adapter uses the same tools as chat. No credential or team switch enters the frame.
  root.SHHackSites = function (app, personal) {
    const sameTeam = (a, b) => a && b && a.event === b.event && a.team === b.team && a.site === b.site;
    return {
      async list(input, scoped) {
        const me = await app.callTool('who_am_i');
        const selected = me.team;
        const sites = input.map(site => {
          const current = selected?.site === site.name;
          return {...site, hack: {kind: 'team', selected: current, team: current ? selected : null},
            label: current ? 'Selected team · ' + selected.team + ' · ' + selected.event : 'Team site · Refresh in chat for dashboard'};
        });
        const errors = [];
        if (personal && !scoped) {
          const [teams, events] = await Promise.allSettled([
            app.callTool('hack_get_my_teams'), app.callTool('hack_list_events')
          ]);
          if (teams.status === 'fulfilled') {
            for (const team of teams.value.teams || []) {
              if (selected && selected.event === team.event_slug && selected.team === team.team_slug) continue;
              sites.push({name: team.team_slug, url: team.site_url, summary: {title: team.team_name, description: team.event_title},
                label: 'Team · ' + team.event_slug + ' · Select in chat for dashboard', hack: {kind: 'team', selected: false}});
            }
          } else errors.push('Could not load other teams.');
          if (events.status === 'fulfilled') {
            for (const event of events.value.events || []) {
              if (event.role !== 'organiser') continue;
              try {
                const website = await app.callTool('hack_get_event_website', {slug: event.slug});
                sites.push({name: event.slug, url: event.url, summary: {title: event.title},
                  label: 'Event website · ' + (website.mode === 'custom' ? 'Custom' : 'Built-in'),
                  hack: {kind: 'event', slug: event.slug, storage: website.has_custom_page === true}});
              } catch { errors.push('Could not load event website: ' + event.slug + '.'); }
            }
          } else errors.push('Could not load event websites.');
        }
        return {sites, status: [selected ? 'Selected team: ' + selected.team + ' · ' + selected.event : 'No team selected. Choose a team in chat to see its dashboard.', ...errors].join(' ')};
      },
      canDashboard: site => site.hack?.kind === 'event' ? site.hack.storage : site.hack?.selected,
      hasAnalytics: site => site.hack?.kind === 'team' && site.hack.selected,
      async check(site) {
        if (site.hack.kind === 'team') {
          const me = await app.callTool('who_am_i');
          if (!sameTeam(site.hack.team, me.team)) throw Error('The selected team changed. Run list_sites in chat to refresh.');
        }
      },
      storage(site, name, args = {}) {
        return site.hack.kind === 'event'
          ? app.callTool('hack_event_' + name, {event: site.hack.slug, ...args})
          : app.callTool(name, {site: site.name, ...args});
      },
      analytics: site => app.callToolResult('site_analytics', {site: site.name, days: 7})
    };
  };
})(globalThis);
