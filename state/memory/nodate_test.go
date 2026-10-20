package memory

import (
	"testing"

	things "github.com/arthursoares/things-cloud-sdk"
)

// Things writes 4001-01-01 (64092211200) where a date is absent, as in the
// deadline and today reference of a recurring template without end.
func TestStateReadsTheYear4001AsNoDate(t *testing.T) {
	t.Parallel()

	state := NewState()
	err := state.Update(
		taskReadItem("dated", task7Kind, things.ItemActionCreated, `{"tt":"Dated","sr":1792540800,"dd":1792540800}`),
		taskReadItem("dated", task7Kind, things.ItemActionModified, `{"sr":64092211200,"dd":64092211200,"sp":64092211200}`),
		taskReadItem("fresh", task7Kind, things.ItemActionCreated, `{"tt":"Fresh","sr":64092211200,"dd":64092211200}`),
	)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	for _, id := range []string{"dated", "fresh"} {
		task := requireTask(t, state, id)
		if task.ScheduledDate != nil || task.DeadlineDate != nil || task.CompletionDate != nil {
			t.Errorf("%s: dates = %v %v %v, want none", id, task.ScheduledDate, task.DeadlineDate, task.CompletionDate)
		}
	}
}
