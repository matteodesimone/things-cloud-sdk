package memory

import (
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
)

// The payloads below are the shapes Things 3 uploads for its recurring
// templates: a yearly rule without end, a daily rule limited to five
// instances that has created all five, and an instance linked through rt.
const (
	yearlyTemplate = `{"tt":"Yearly","tp":0,"st":2,"ss":0,"icc":11,"rt":[],"rp":null,
		"rr":{"ed":64092211200,"fa":1,"fu":4,"ia":1477872000,"of":[{"dy":-1,"mo":9}],"rc":0,"rrv":4,"sr":1468108800,"tp":0,"ts":-30}}`
	finishedTemplate = `{"tt":"Five days","tp":0,"st":2,"ss":0,"icc":5,"rt":[],"rp":null,
		"rr":{"fa":1,"fu":16,"ia":1743724800,"of":[{"dy":0}],"rc":5,"rrv":4,"sr":1743724800,"tp":0,"ts":0}}`
	recurringInstance = `{"tt":"Yearly","tp":0,"st":1,"ss":0,"rt":["yearly"],"rr":null}`
)

func TestStateKeepsTheRecurrenceRule(t *testing.T) {
	t.Parallel()

	state := NewState()
	err := state.Update(
		taskReadItem("yearly", task7Kind, things.ItemActionCreated, yearlyTemplate),
		taskReadItem("five", task7Kind, things.ItemActionCreated, finishedTemplate),
		taskReadItem("instance", task7Kind, things.ItemActionCreated, recurringInstance),
	)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	yearly := requireTask(t, state, "yearly")
	if yearly.Repeater == nil {
		t.Fatalf("yearly template lost its rule")
	}
	if yearly.Repeater.FrequencyUnit != things.FrequencyUnitYearly || len(yearly.Repeater.DetailConfiguration) != 1 {
		t.Fatalf("yearly rule = %+v", yearly.Repeater)
	}
	if !yearly.IsRecurringTemplate() || yearly.IsRecurringInstance() || !yearly.IsRecurring() {
		t.Fatalf("yearly template classified as template=%v instance=%v", yearly.IsRecurringTemplate(), yearly.IsRecurringInstance())
	}
	if yearly.InstanceCreationCount != 11 {
		t.Fatalf("icc = %d, want 11", yearly.InstanceCreationCount)
	}

	instance := requireTask(t, state, "instance")
	if instance.IsRecurringTemplate() || !instance.IsRecurringInstance() || !instance.IsRecurring() {
		t.Fatalf("instance classified as template=%v instance=%v", instance.IsRecurringTemplate(), instance.IsRecurringInstance())
	}
	if tmpl := state.RecurrenceTemplate(instance); tmpl != yearly {
		t.Fatalf("template of the instance = %v, want the yearly template", tmpl)
	}

	day := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	if yearly.RecurrenceEnded(day) {
		t.Fatalf("a yearly rule without end is not finished")
	}
	if !requireTask(t, state, "five").RecurrenceEnded(day) {
		t.Fatalf("a rule of five instances that created five is finished")
	}
}

func TestStateClearsTheRecurrenceRule(t *testing.T) {
	t.Parallel()

	state := NewState()
	err := state.Update(
		taskReadItem("yearly", task7Kind, things.ItemActionCreated, yearlyTemplate),
		taskReadItem("yearly", task7Kind, things.ItemActionModified, `{"rr":null}`),
	)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if task := requireTask(t, state, "yearly"); task.Repeater != nil || task.IsRecurringTemplate() {
		t.Fatalf("an explicit null rr must clear the rule, got %+v", task.Repeater)
	}
}
