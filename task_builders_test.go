package thingscloud

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"
)

var (
	builderNow   = time.Date(2026, 10, 21, 9, 30, 0, 0, time.UTC)
	builderToday = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
)

// checkWritable runs the item through the same policy History.Write applies
// before posting, and returns its envelope.
func checkWritable(t *testing.T, item Identifiable) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{item.UUID(): item})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateTaskWrites(body); err != nil {
		t.Fatalf("the write policy refuses %s: %v", body, err)
	}
	var envelopes map[string]map[string]any
	if err := json.Unmarshal(body, &envelopes); err != nil {
		t.Fatal(err)
	}
	return envelopes[item.UUID()]
}

func payloadKeys(envelope map[string]any) []string {
	var keys []string
	for k := range envelope["p"].(map[string]any) {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestNewTaskCreateBuildsTheVerifiedShape(t *testing.T) {
	t.Parallel()

	area, project := NewUUID(), NewUUID()
	day := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		create TaskCreate
		st     float64
		sr     any
	}{
		{"inbox", TaskCreate{Title: "In the inbox", Schedule: TaskScheduleInbox}, 0, nil},
		{"area", TaskCreate{Title: "In an area", AreaID: area, Schedule: TaskScheduleAnytime, Note: "a note"}, 1, nil},
		{"project, future date", TaskCreate{Title: "Later", ProjectID: project, ScheduledDate: &day, DeadlineDate: &day}, 2, float64(day.Unix())},
		{"area, today", TaskCreate{Title: "Now", AreaID: area, ScheduledDate: &builderToday}, 1, float64(builderToday.Unix())},
		{"project itself", TaskCreate{Title: "A project", Type: TaskTypeProject, AreaID: area, Schedule: TaskScheduleAnytime}, 1, nil},
		{"project, schedule left out", TaskCreate{Title: "A project", Type: TaskTypeProject, AreaID: area}, 1, nil},
		{"area, schedule left out", TaskCreate{Title: "Filed", AreaID: area}, 1, nil},
		{"in a project, schedule left out", TaskCreate{Title: "Filed", ProjectID: project}, 1, nil},
	}
	for _, c := range cases {
		c.create.Now, c.create.Today = builderNow, builderToday
		item, err := NewTaskCreate(NewUUID(), c.create)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		env := checkWritable(t, item)
		p := env["p"].(map[string]any)
		if env["e"] != "Task7" || env["t"] != float64(0) {
			t.Errorf("%s: envelope %v/%v, want Task7 create", c.name, env["e"], env["t"])
		}
		if p["st"] != c.st || p["sr"] != c.sr || p["tir"] != c.sr || p["tt"] != c.create.Title || p["md"] != nil {
			t.Errorf("%s: st %v sr %v tir %v tt %v md %v", c.name, p["st"], p["sr"], p["tir"], p["tt"], p["md"])
		}
	}
}

func TestNewTaskCreateRefusesWhatThePolicyRefuses(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]TaskCreate{
		"area and project":    {Title: "x", AreaID: NewUUID(), ProjectID: NewUUID()},
		"invalid area":        {Title: "x", AreaID: "not-a-uuid"},
		"project with a date": {Title: "x", Type: TaskTypeProject, ScheduledDate: &builderToday},
		"project in someday":  {Title: "x", Type: TaskTypeProject, Schedule: TaskScheduleSomeday},
		"heading":             {Title: "x", Type: TaskTypeHeading, Schedule: TaskScheduleAnytime},
	} {
		c.Now, c.Today = builderNow, builderToday
		if _, err := NewTaskCreate(NewUUID(), c); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestTaskUpdateMovesWithoutTouchingTheSchedule(t *testing.T) {
	t.Parallel()

	area, project := NewUUID(), NewUUID()
	cases := []struct {
		name   string
		update *TaskUpdate
		want   map[string]any
	}{
		{"to an area", NewTaskUpdate(NewUUID(), builderNow).MoveToArea(area),
			map[string]any{"ar": []any{area}, "pr": []any{}, "agr": []any{}}},
		{"to a project", NewTaskUpdate(NewUUID(), builderNow).MoveToProject(project),
			map[string]any{"ar": []any{}, "pr": []any{project}, "agr": []any{}}},
		{"out of every container", NewTaskUpdate(NewUUID(), builderNow).MoveOut(),
			map[string]any{"ar": []any{}, "pr": []any{}, "agr": []any{}}},
	}
	for _, c := range cases {
		env := checkWritable(t, c.update)
		if got := payloadKeys(env); !reflect.DeepEqual(got, []string{"agr", "ar", "md", "pr"}) {
			t.Errorf("%s: keys %v, want only md and the containers", c.name, got)
		}
		p := env["p"].(map[string]any)
		for k, v := range c.want {
			if !reflect.DeepEqual(p[k], v) {
				t.Errorf("%s: %s = %v, want %v", c.name, k, p[k], v)
			}
		}
	}
}

func TestTaskUpdateFields(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		update *TaskUpdate
		want   map[string]any
	}{
		{"title and note", NewTaskUpdate(NewUUID(), builderNow).Title("New").Note("text"),
			map[string]any{"tt": "New", "nt": map[string]any{"_t": "tx", "ch": float64(crc32Of("text")), "v": "text", "t": float64(1)}}},
		{"empty note", NewTaskUpdate(NewUUID(), builderNow).Note(""),
			map[string]any{"nt": map[string]any{"_t": "tx", "ch": float64(0), "v": "", "t": float64(1)}}},
		{"future date", NewTaskUpdate(NewUUID(), builderNow).ScheduleDate(day, builderToday),
			map[string]any{"st": float64(2), "sr": float64(day.Unix()), "tir": float64(day.Unix())}},
		{"today", NewTaskUpdate(NewUUID(), builderNow).ScheduleDate(builderToday, builderToday),
			map[string]any{"st": float64(1), "sr": float64(builderToday.Unix()), "tir": float64(builderToday.Unix())}},
		{"no date", NewTaskUpdate(NewUUID(), builderNow).Schedule(TaskScheduleAnytime),
			map[string]any{"st": float64(1), "sr": nil, "tir": nil}},
		// A new deadline clears the suppression from Today, as the app
		// does (captured 2026-10-21).
		{"deadline", NewTaskUpdate(NewUUID(), builderNow).Deadline(&day),
			map[string]any{"dd": float64(day.Unix()), "dds": nil}},
		{"no deadline", NewTaskUpdate(NewUUID(), builderNow).Deadline(nil),
			map[string]any{"dd": nil}},
		{"complete", NewTaskUpdate(NewUUID(), builderNow).Complete(builderNow),
			map[string]any{"ss": float64(3), "sp": float64(builderNow.Unix())}},
		{"reopen", NewTaskUpdate(NewUUID(), builderNow).Reopen(),
			map[string]any{"ss": float64(0), "sp": nil}},
		{"trash", NewTaskUpdate(NewUUID(), builderNow).Trash(true),
			map[string]any{"tr": true}},
	}
	for _, c := range cases {
		env := checkWritable(t, c.update)
		p := env["p"].(map[string]any)
		if env["e"] != "Task7" || env["t"] != float64(1) || p["md"] != float64(builderNow.Unix()) {
			t.Errorf("%s: envelope %v/%v md %v", c.name, env["e"], env["t"], p["md"])
		}
		if len(p) != len(c.want)+1 {
			t.Errorf("%s: payload %v carries more than %v", c.name, p, c.want)
		}
		for k, v := range c.want {
			if !reflect.DeepEqual(p[k], v) {
				t.Errorf("%s: %s = %#v, want %#v", c.name, k, p[k], v)
			}
		}
	}
}

// A modification may clear the deadline suppression (dds: null), as the app
// does with a new deadline; setting it is outside the verified scope.
func TestModificationDeadlineSuppressionOnlyNull(t *testing.T) {
	t.Parallel()
	if err := validateTask7Modification(map[string]json.RawMessage{"md": json.RawMessage(`1700000000`), "dds": json.RawMessage(`null`)}); err != nil {
		t.Errorf("dds null refused: %v", err)
	}
	if err := validateTask7Modification(map[string]json.RawMessage{"md": json.RawMessage(`1700000000`), "dds": json.RawMessage(`1700000000`)}); err == nil {
		t.Error("a dds day was accepted")
	}
}

func TestNewAreaAndChecklistItemCreate(t *testing.T) {
	t.Parallel()

	area := NewAreaCreate(NewUUID(), "Projects")
	env := checkWritable(t, area)
	if env["e"] != "Area3" || env["t"] != float64(0) {
		t.Fatalf("area envelope %v/%v", env["e"], env["t"])
	}
	if got := payloadKeys(env); !reflect.DeepEqual(got, []string{"ix", "tg", "tt", "xx"}) {
		t.Fatalf("area keys %v", got)
	}

	task := NewUUID()
	item := NewChecklistItemCreate(NewUUID(), task, "A step", 2, builderNow)
	env = checkWritable(t, item)
	p := env["p"].(map[string]any)
	if env["e"] != "ChecklistItem3" || env["t"] != float64(0) {
		t.Fatalf("checklist envelope %v/%v", env["e"], env["t"])
	}
	if got := payloadKeys(env); !reflect.DeepEqual(got, []string{"cd", "ix", "lt", "md", "sp", "ss", "ts", "tt", "xx"}) {
		t.Fatalf("checklist keys %v", got)
	}
	if !reflect.DeepEqual(p["ts"], []any{task}) || p["tt"] != "A step" || p["ix"] != float64(2) || p["md"] != nil {
		t.Fatalf("checklist payload %v", p)
	}
}
