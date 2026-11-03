package model

import "regexp"

var uuidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidUUID reports whether s has the UUID shape accepted for ID values.
func ValidUUID(s string) bool { return uuidShape.MatchString(s) }
