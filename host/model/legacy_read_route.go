package model

type LegacyDataRoute uint8

const (
	// LegacyDataPersonal delegates to the person's record read.
	LegacyDataPersonal LegacyDataRoute = iota
	// LegacyDataContent reads a page-info document.
	LegacyDataContent
	// LegacyDataUndeclared rejects a name without a readable kind.
	LegacyDataUndeclared
	// LegacyDataCount reads the number of live items.
	LegacyDataCount
	// LegacyDataList delegates to the list read.
	LegacyDataList
)
