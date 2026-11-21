package servertest

import (
	"strconv"
	"testing"
	"time"
)

func UniqueLabel(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 36)
}
