package thingscloud

import (
	"testing"
	"time"
)

func TestTaskRecurrenceEnded(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	ts := func(y int, m time.Month, d int) *Timestamp {
		v := Timestamp(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
		return &v
	}
	count := func(n int64) *int64 { return &n }

	cases := []struct {
		name string
		task Task
		want bool
	}{
		{"ordinary task", Task{}, false},
		{"instance", Task{RecurrenceIDs: []string{"tmpl"}}, false},
		{"no end", Task{Repeater: &RepeaterConfiguration{LastScheduledAt: ts(4001, 1, 1), RepeatCount: count(0)}, InstanceCreationCount: 11}, false},
		{"count not reached", Task{Repeater: &RepeaterConfiguration{RepeatCount: count(5)}, InstanceCreationCount: 4}, false},
		{"count reached", Task{Repeater: &RepeaterConfiguration{RepeatCount: count(5)}, InstanceCreationCount: 5}, true},
		{"end date passed", Task{Repeater: &RepeaterConfiguration{LastScheduledAt: ts(2024, 12, 25), RepeatCount: count(0)}, InstanceCreationCount: 7}, true},
		{"end date today", Task{Repeater: &RepeaterConfiguration{LastScheduledAt: ts(2026, 10, 21)}}, false},
		{"end date ahead", Task{Repeater: &RepeaterConfiguration{LastScheduledAt: ts(2027, 1, 1)}}, false},
	}
	for _, c := range cases {
		if got := c.task.RecurrenceEnded(day); got != c.want {
			t.Errorf("%s: RecurrenceEnded = %v, want %v", c.name, got, c.want)
		}
	}
}
