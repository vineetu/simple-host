package storage

import rules "github.com/vsriram/simple-host/internal/rules/storage"

// ExtractBudget returns the existing unpacked archive budget for a website upload.
func ExtractBudget(limit int64, override bool) int64 { return rules.ExtractBudget(limit, override) }
