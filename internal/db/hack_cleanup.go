package db

import repo "github.com/vsriram/simple-host/internal/repository/hackevents"

var ListEventsForSiteCleanup = repo.ListEventsForSiteCleanup
var MarkEventRemovalWarned = repo.MarkEventRemovalWarned
var MarkEventSitesRemoved = repo.MarkEventSitesRemoved
var SetEventKeepSites = repo.SetEventKeepSites
