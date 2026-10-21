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

// A template with deadlines («Add deadlines») writes dd at 4001-01-01, and
// tir is the start of its next instance (captured 2026-10-21): the state
// keeps both, and a real deadline or a null clears the mark.
func TestStateKeepsTheDeadlinesOfATemplate(t *testing.T) {
	t.Parallel()

	state := NewState()
	err := state.Update(
		taskReadItem("deadlines", task7Kind, things.ItemActionCreated, `{"tt":"Bollo","tp":0,"st":2,"ss":0,"icc":1,"rt":[],"rp":null,"dd":64092211200,"tir":1824076800,
			"rr":{"ed":64092211200,"fa":1,"fu":4,"ia":1792540800,"of":[{"dy":25,"mo":9}],"rc":0,"rrv":4,"sr":1792368000,"tp":0,"ts":-5}}`),
		taskReadItem("plain", task7Kind, things.ItemActionCreated, `{"tt":"Daily","tp":0,"st":2,"ss":0,"rt":[],"rp":null,"dd":null,
			"rr":{"ed":64092211200,"fa":1,"fu":16,"ia":1792540800,"of":[{"dy":0}],"rc":0,"rrv":4,"sr":1792540800,"tp":0,"ts":0}}`),
	)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	tmpl := requireTask(t, state, "deadlines")
	if !tmpl.RepeatsWithDeadlines || tmpl.DeadlineDate != nil {
		t.Fatalf("deadlines = %v, deadline %v", tmpl.RepeatsWithDeadlines, tmpl.DeadlineDate)
	}
	if want := time.Date(2027, 10, 21, 0, 0, 0, 0, time.UTC); tmpl.TodayIndexReference == nil || !tmpl.TodayIndexReference.Equal(want) {
		t.Fatalf("tir = %v, want %v", tmpl.TodayIndexReference, want)
	}
	if tmpl.Repeater.TimeShift != -5 {
		t.Fatalf("ts = %d", tmpl.Repeater.TimeShift)
	}
	if requireTask(t, state, "plain").RepeatsWithDeadlines {
		t.Fatalf("a template without deadlines has them")
	}
	if err := state.Update(taskReadItem("deadlines", task7Kind, things.ItemActionModified, `{"dd":null,"tir":null}`)); err != nil {
		t.Fatal(err)
	}
	if tmpl := requireTask(t, state, "deadlines"); tmpl.RepeatsWithDeadlines || tmpl.TodayIndexReference != nil {
		t.Fatalf("after dd null: deadlines %v, tir %v", tmpl.RepeatsWithDeadlines, tmpl.TodayIndexReference)
	}
}
