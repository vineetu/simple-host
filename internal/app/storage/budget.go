package storage

import rules "github.com/vsriram/simple-host/internal/rules/storage"

// ExtractBudget resolves the unpacked-file budget for a site-specific override.
func ExtractBudget(limit int64, override bool) int64 { return rules.ExtractBudget(limit, override) }
