package sync

import (
	"encoding/json"
	"testing"

	things "github.com/arthursoares/things-cloud-sdk"
)

func TestApplyTaskPayloadReadsTheYear4001AsNoDate(t *testing.T) {
	t.Parallel()

	var p things.TaskActionItemPayload
	if err := json.Unmarshal([]byte(`{"tt":"Template","sr":64092211200,"dd":64092211200,"sp":64092211200}`), &p); err != nil {
		t.Fatal(err)
	}
	task := applyTaskPayload(nil, "template", p)
	if task.ScheduledDate != nil || task.DeadlineDate != nil || task.CompletionDate != nil {
		t.Fatalf("dates = %v %v %v, want none", task.ScheduledDate, task.DeadlineDate, task.CompletionDate)
	}
}
