// Package visitors turns the opaque visitor ids in a site's saved data into
// the people behind them, for the site's owner only: GET .../storage/visitors
// (ids to sign-in emails and back, only for people who signed in on this
// site) and GET .../storage/people (the pick list of people with data or
// named-viewer status). Who a new row is for (ResolveOwnerFor) is the data
// core's, because the row writes use it.
package visitors
