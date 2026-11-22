// Package files is a site's file buckets: list, read, store and delete
// objects through the bucket's rules, and the owner's signed download links
// (/v1/storage-download), with their routes. Who uploaded each file is kept
// in site_storage_files (the data core's repo, because declaring a resource
// clears it).
package files
