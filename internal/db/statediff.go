package db

import storagerules "github.com/vsriram/simple-host/internal/rules/storage"

type diffEntry = storagerules.LegacyDiffEntry

var errBrokenHistory = storagerules.ErrLegacyBrokenHistory

func reverseDiff(before, after any) ([]diffEntry, error) {
	return storagerules.LegacyReverseDiff(before, after)
}

func applyReverse(doc any, entries []diffEntry) (any, error) {
	return storagerules.LegacyApplyReverse(doc, entries)
}

func decodeNumbers(raw []byte) (any, error) {
	return storagerules.LegacyDecodeNumbers(raw)
}
