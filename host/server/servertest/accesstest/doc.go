// Package accesstest holds Access's journeys: route tests that cross its
// features (sign in, then use another feature; a connector token on the REST
// routes; the gate's key in the connector), on every feature composed by
// accesshttp over the shared fixture (host/access/featuretest). A test of one
// feature lives in that feature's package and mounts only that feature.
package accesstest
