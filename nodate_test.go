package thingscloud

import (
	"testing"
	"time"
)

func TestTimestampDate(t *testing.T) {
	t.Parallel()

	noDate := Timestamp(time.Unix(64092211200, 0).UTC())
	if got := noDate.Date(); got != nil {
		t.Fatalf("4001-01-01 = %v, want no date", got)
	}
	day := Timestamp(time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC))
	if got := day.Date(); got == nil || !got.Equal(time.Time(day)) {
		t.Fatalf("2026-10-21 = %v", got)
	}
	var missing *Timestamp
	if got := missing.Date(); got != nil {
		t.Fatalf("nil timestamp = %v", got)
	}
}
