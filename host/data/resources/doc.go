// Package resources declares a site's saved data: PUT, GET and DELETE
// .../storage/resources, the rules a resource is saved with (decoded,
// validated against the bindings schema and the worst case, the tables they
// need prepared) and the listing. Loading a resource and its rules JSON are
// the data core's, because every storage call resolves one.
package resources
