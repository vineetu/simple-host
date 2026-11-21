package web

import "github.com/vsriram/simple-host/host/sites/photos"

// The handler photo tests still call the checker by its old name.
var validatePhoto = photos.Validate
