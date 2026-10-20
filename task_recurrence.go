package thingscloud

import "time"

// IsRecurringTemplate reports whether the task is a recurring template: the
// task that holds a recurrence rule and creates the instances. Things does
// not show templates in its lists, only the instances they create.
func (t *Task) IsRecurringTemplate() bool { return t.Repeater != nil }

// IsRecurringInstance reports whether the task was created by a recurring
// template, which its rt relationship names.
func (t *Task) IsRecurringInstance() bool { return len(t.RecurrenceIDs) > 0 }

// IsRecurring reports whether the task is a recurring template or one of
// its instances.
func (t *Task) IsRecurring() bool { return t.IsRecurringTemplate() || t.IsRecurringInstance() }

// RecurrenceEnded reports whether the task is a recurring template whose
// rule creates no more instances on or after day: it has created as many
// instances as its repeat count (rc) allows, or its end date (ed) is before
// day. Things keeps such templates in the history but no longer shows them.
func (t *Task) RecurrenceEnded(day time.Time) bool {
	r := t.Repeater
	if r == nil {
		return false
	}
	if r.RepeatCount != nil && *r.RepeatCount > 0 && int64(t.InstanceCreationCount) >= *r.RepeatCount {
		return true
	}
	if r.LastScheduledAt != nil && !r.IsNeverending() {
		end := r.LastScheduledAt.Time()
		y, m, d := day.Date()
		return end.Before(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
	}
	return false
}
