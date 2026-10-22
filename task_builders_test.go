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

var nineOClock = 9 * time.Hour

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
		// «Mark as canceled» in the app (captured 2026-10-21).
		{"cancel", NewTaskUpdate(NewUUID(), builderNow).Cancel(builderNow),
			map[string]any{"ss": float64(2), "sp": float64(builderNow.Unix())}},
		// A reminder is the time of day on the start date, in seconds from
		// midnight in the local zone; none clears it (captured 2026-10-21).
		{"reminder", NewTaskUpdate(NewUUID(), builderNow).Reminder(&nineOClock),
			map[string]any{"ato": float64(32400)}},
		{"no reminder", NewTaskUpdate(NewUUID(), builderNow).Reminder(nil),
			map[string]any{"ato": nil}},
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

// A modification may cancel (ss 2 with its stop date, as «Mark as
// canceled» writes it) and set or clear the reminder (ato, the seconds of
// a day); anything else around them stays outside the verified scope.
func TestModificationCancelAndReminder(t *testing.T) {
	t.Parallel()
	md := json.RawMessage(`1700000000`)
	for name, p := range map[string]map[string]json.RawMessage{
		"cancel":      {"md": md, "ss": json.RawMessage(`2`), "sp": md},
		"reminder":    {"md": md, "ato": json.RawMessage(`32400`)},
		"no reminder": {"md": md, "ato": json.RawMessage(`null`)},
	} {
		if err := validateTask7Modification(p); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
	for name, p := range map[string]map[string]json.RawMessage{
		"cancel without its stop date": {"md": md, "ss": json.RawMessage(`2`), "sp": json.RawMessage(`null`)},
		"a reminder past the day":      {"md": md, "ato": json.RawMessage(`86400`)},
		"a negative reminder":          {"md": md, "ato": json.RawMessage(`-1`)},
		"a reminder that is no number": {"md": md, "ato": json.RawMessage(`"9:00"`)},
	} {
		if err := validateTask7Modification(p); err == nil {
			t.Errorf("%s accepted", name)
		}
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

// A recurring template with deadlines («Add deadlines», captured
// 2026-10-21, commits 97, 100 and 103 of the test account): the whole
// creation the app writes, with the start of the next instance in tir and
// icsd, and md at the creation time.
func TestNewDeadlineTemplateCreateBuildsTheCapturedShape(t *testing.T) {
	t.Parallel()

	project := NewUUID()
	march := int64(2)
	start := time.Date(2027, 3, 20, 0, 0, 0, 0, time.UTC)
	item, err := NewDeadlineTemplateCreate(NewUUID(), DeadlineTemplate{
		Title: "Prova marzo", ProjectID: project,
		Rule:  DeadlineRule{Unit: FrequencyUnitYearly, Amplitude: 1, Day: 29, Month: &march, Lead: 10, Anchor: builderToday, Reference: builderToday},
		Start: start, Now: builderNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := checkWritable(t, item)
	got, _ := json.Marshal(env["p"])
	now := float64(builderNow.UnixNano()) / 1e9
	want := map[string]any{
		"tp": 0, "sr": nil, "dds": nil, "rt": []string{}, "rmd": nil, "ss": 0, "tr": false, "dl": []string{}, "icp": false, "st": 2, "ar": []string{},
		"tt": "Prova marzo", "do": 0, "lai": nil, "tir": start.Unix(), "tg": []string{}, "agr": []string{}, "ix": 0, "cd": now, "lt": false, "icc": 0,
		"md": now, "ti": 0, "dd": 64092211200, "ato": nil, "nt": map[string]any{"_t": "tx", "ch": 0, "v": "", "t": 1}, "icsd": start.Unix(),
		"pr": []string{project}, "rp": nil, "acrd": nil, "sp": nil, "sb": 0,
		"rr": map[string]any{"ia": builderToday.Unix(), "of": []map[string]any{{"dy": 29, "mo": 2}}, "rrv": 4, "ts": -10, "fu": 4, "sr": builderToday.Unix(), "fa": 1, "tp": 0, "rc": 0, "ed": 64092211200},
		"xx": map[string]any{"sn": map[string]any{}, "_t": "oo"},
	}
	wantJSON, _ := json.Marshal(want)
	var a, b any
	json.Unmarshal(got, &a)
	json.Unmarshal(wantJSON, &b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("payload\n got %s\nwant %s", got, wantJSON)
	}

	// Monthly, every 3 months: no month in the day of the rule.
	item, err = NewDeadlineTemplateCreate(NewUUID(), DeadlineTemplate{
		Title: "Prova trimestrale", Note: "una nota", ProjectID: project,
		Rule:  DeadlineRule{Unit: FrequencyUnitMonthly, Amplitude: 3, Day: 15, Lead: 5, Anchor: builderToday, Reference: builderToday},
		Start: time.Date(2027, 1, 11, 0, 0, 0, 0, time.UTC), Now: builderNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := checkWritable(t, item)["p"].(map[string]any)["rr"].(map[string]any)
	if rr["fu"] != float64(8) || rr["fa"] != float64(3) || !reflect.DeepEqual(rr["of"], []any{map[string]any{"dy": float64(15)}}) || rr["ts"] != float64(-5) {
		t.Errorf("monthly rr %v", rr)
	}

	for name, c := range map[string]DeadlineTemplate{
		"no project":      {Title: "x", Rule: DeadlineRule{Unit: FrequencyUnitMonthly, Amplitude: 1, Day: 15}},
		"daily":           {Title: "x", ProjectID: project, Rule: DeadlineRule{Unit: FrequencyUnitDaily, Amplitude: 1}},
		"weekly":          {Title: "x", ProjectID: project, Rule: DeadlineRule{Unit: FrequencyUnitWeekly, Amplitude: 1}},
		"yearly no month": {Title: "x", ProjectID: project, Rule: DeadlineRule{Unit: FrequencyUnitYearly, Amplitude: 1, Day: 4}},
		"monthly month":   {Title: "x", ProjectID: project, Rule: DeadlineRule{Unit: FrequencyUnitMonthly, Amplitude: 1, Day: 4, Month: &march}},
		"negative lead":   {Title: "x", ProjectID: project, Rule: DeadlineRule{Unit: FrequencyUnitMonthly, Amplitude: 1, Day: 4, Lead: -1}},
		"no amplitude":    {Title: "x", ProjectID: project, Rule: DeadlineRule{Unit: FrequencyUnitMonthly, Day: 4}},
		"day 31":          {Title: "x", ProjectID: project, Rule: DeadlineRule{Unit: FrequencyUnitMonthly, Amplitude: 1, Day: 31}},
	} {
		c.Rule.Anchor, c.Rule.Reference, c.Start, c.Now = builderToday, builderToday, builderToday, builderNow
		if _, err := NewDeadlineTemplateCreate(NewUUID(), c); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// The rule of a template with deadlines changes as «Reschedule» writes it
// (captured 2026-10-21, commit 100): the whole rule, and tir and icsd at
// the start of the next instance, without the schedule.
func TestTaskUpdateRepeat(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	u := NewTaskUpdate(NewUUID(), builderNow).Repeat(DeadlineRule{Unit: FrequencyUnitMonthly, Amplitude: 1, Day: 19, Lead: 5, Anchor: builderToday, Reference: builderToday}, start)
	env := checkWritable(t, u)
	if keys := payloadKeys(env); !reflect.DeepEqual(keys, []string{"icsd", "md", "rr", "tir"}) {
		t.Errorf("keys %v", keys)
	}
	p := env["p"].(map[string]any)
	if p["tir"] != float64(start.Unix()) || p["icsd"] != float64(start.Unix()) {
		t.Errorf("tir %v icsd %v", p["tir"], p["icsd"])
	}
	for name, fields := range map[string]string{
		"rr without tir":       `"rr":{"ia":1792540800,"of":[{"dy":19}],"tp":0,"ts":-5,"fu":8,"rc":0,"sr":1792540800,"rrv":4,"fa":1,"ed":64092211200},"icsd":1795219200`,
		"tir and icsd apart":   `"rr":{"ia":1792540800,"of":[{"dy":19}],"tp":0,"ts":-5,"fu":8,"rc":0,"sr":1792540800,"rrv":4,"fa":1,"ed":64092211200},"tir":1795219200,"icsd":1795305600`,
		"rr with the schedule": `"rr":{"ia":1792540800,"of":[{"dy":19}],"tp":0,"ts":-5,"fu":8,"rc":0,"sr":1792540800,"rrv":4,"fa":1,"ed":64092211200},"tir":1795219200,"icsd":1795219200,"st":2,"sr":1795219200`,
		// A daily rule is one of a template without deadlines: the policy
		// takes it, and the preflight refuses it on a template with
		// deadlines (TestHistoryWriteTask7RepeatingTemplate).
		"a daily rr in advance":   `"rr":{"ia":1792540800,"of":[{"dy":0}],"tp":0,"ts":-5,"fu":16,"rc":0,"sr":1792540800,"rrv":4,"fa":1,"ed":64092211200},"tir":1795219200,"icsd":1795219200`,
		"a rule that ends":        `"rr":{"ia":1792540800,"of":[{"dy":19}],"tp":0,"ts":-5,"fu":8,"rc":0,"sr":1792540800,"rrv":4,"fa":1,"ed":1800000000},"tir":1795219200,"icsd":1795219200`,
		"a rule after completion": `"rr":{"ia":1792540800,"of":[{"dy":19}],"tp":1,"ts":-5,"fu":8,"rc":0,"sr":1792540800,"rrv":4,"fa":1,"ed":64092211200},"tir":1795219200,"icsd":1795219200`,
		"icsd alone":              `"icsd":1795219200`,
		"rr null":                 `"rr":null,"tir":1795219200,"icsd":1795219200`,
	} {
		body := []byte(`{"` + NewUUID() + `":` + task7UpdateBody(fields) + `}`)
		if _, err := validateTaskWrites(body); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A recurring template on fixed dates without deadlines (captured
// 2026-10-22, «Prova settimanale», commit 253): the whole creation the app
// writes, scheduled (st 2) without a start date, the first occurrence in
// tir and icsd, the reminder in ato, md at the creation time, and a rule
// that does not start in advance (ts 0), with or without an end.
func TestNewRepeatingTaskCreateBuildsTheCapturedShape(t *testing.T) {
	t.Parallel()

	area := NewUUID()
	first := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	nine := 9 * time.Hour
	item, err := NewRepeatingTaskCreate(NewUUID(), RepeatingTask{
		Title: "Prova settimanale", AreaID: area,
		Rule:  RepeatingRule{Unit: FrequencyUnitWeekly, Amplitude: 1, Weekdays: []time.Weekday{time.Tuesday}, Anchor: builderToday, Reference: builderToday},
		First: first, Reminder: &nine, Now: builderNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := checkWritable(t, item)
	got, _ := json.Marshal(env["p"])
	now := float64(builderNow.UnixNano()) / 1e9
	want := map[string]any{
		"tp": 0, "sr": nil, "dds": nil, "rt": []string{}, "rmd": nil, "ss": 0, "tr": false, "dl": []string{}, "icp": false, "st": 2, "ar": []string{area},
		"tt": "Prova settimanale", "do": 0, "lai": nil, "tir": first.Unix(), "tg": []string{}, "agr": []string{}, "ix": 0, "cd": now, "lt": false, "icc": 0,
		"md": now, "ti": 0, "dd": nil, "ato": 32400, "nt": map[string]any{"_t": "tx", "ch": 0, "v": "", "t": 1}, "icsd": first.Unix(),
		"pr": []string{}, "rp": nil, "acrd": nil, "sp": nil, "sb": 0,
		"rr": map[string]any{"ia": builderToday.Unix(), "of": []map[string]any{{"wd": 2}}, "rrv": 4, "ts": 0, "fu": 256, "sr": builderToday.Unix(), "fa": 1, "tp": 0, "rc": 0, "ed": 64092211200},
		"xx": map[string]any{"sn": map[string]any{}, "_t": "oo"},
	}
	wantJSON, _ := json.Marshal(want)
	var a, b any
	json.Unmarshal(got, &a)
	json.Unmarshal(wantJSON, &b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("payload\n got %s\nwant %s", got, wantJSON)
	}

	// In a project, every two weeks on two days, with an end, no reminder
	// (the rule of commit 255 and the end of commit 254); every three days.
	project := NewUUID()
	end := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	item, err = NewRepeatingTaskCreate(NewUUID(), RepeatingTask{
		Title: "Prova", Note: "una nota", ProjectID: project,
		Rule:  RepeatingRule{Unit: FrequencyUnitWeekly, Amplitude: 2, Weekdays: []time.Weekday{time.Monday, time.Thursday}, End: &end, Anchor: first, Reference: builderToday},
		First: first, Now: builderNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	p := checkWritable(t, item)["p"].(map[string]any)
	rr := p["rr"].(map[string]any)
	if !reflect.DeepEqual(p["pr"], []any{project}) || !reflect.DeepEqual(p["ar"], []any{}) || p["ato"] != nil ||
		rr["fa"] != float64(2) || rr["ed"] != float64(end.Unix()) || !reflect.DeepEqual(rr["of"], []any{map[string]any{"wd": float64(1)}, map[string]any{"wd": float64(4)}}) {
		t.Errorf("in a project: %v", p)
	}
	item, err = NewRepeatingTaskCreate(NewUUID(), RepeatingTask{
		Title: "Prova", AreaID: area, Rule: RepeatingRule{Unit: FrequencyUnitDaily, Amplitude: 3, Anchor: builderToday, Reference: builderToday},
		First: builderToday, Now: builderNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rr := checkWritable(t, item)["p"].(map[string]any)["rr"].(map[string]any); rr["fu"] != float64(16) || !reflect.DeepEqual(rr["of"], []any{map[string]any{"dy": float64(0)}}) {
		t.Errorf("daily rr %v", rr)
	}

	march := int64(2)
	late := 25 * time.Hour
	for name, c := range map[string]RepeatingTask{
		"no place":          {Title: "x", Rule: RepeatingRule{Unit: FrequencyUnitDaily, Amplitude: 1}},
		"two places":        {Title: "x", AreaID: area, ProjectID: project, Rule: RepeatingRule{Unit: FrequencyUnitDaily, Amplitude: 1}},
		"weekly no day":     {Title: "x", AreaID: area, Rule: RepeatingRule{Unit: FrequencyUnitWeekly, Amplitude: 1}},
		"weekly twice":      {Title: "x", AreaID: area, Rule: RepeatingRule{Unit: FrequencyUnitWeekly, Amplitude: 1, Weekdays: []time.Weekday{time.Monday, time.Monday}}},
		"weekly unordered":  {Title: "x", AreaID: area, Rule: RepeatingRule{Unit: FrequencyUnitWeekly, Amplitude: 1, Weekdays: []time.Weekday{time.Thursday, time.Monday}}},
		"weekly day 7":      {Title: "x", AreaID: area, Rule: RepeatingRule{Unit: FrequencyUnitWeekly, Amplitude: 1, Weekdays: []time.Weekday{7}}},
		"yearly no month":   {Title: "x", AreaID: area, Rule: RepeatingRule{Unit: FrequencyUnitYearly, Amplitude: 1, Day: 4}},
		"monthly month":     {Title: "x", AreaID: area, Rule: RepeatingRule{Unit: FrequencyUnitMonthly, Amplitude: 1, Day: 4, Month: &march}},
		"no amplitude":      {Title: "x", AreaID: area, Rule: RepeatingRule{Unit: FrequencyUnitDaily}},
		"reminder too late": {Title: "x", AreaID: area, Rule: RepeatingRule{Unit: FrequencyUnitDaily, Amplitude: 1}, Reminder: &late},
	} {
		c.Rule.Anchor, c.Rule.Reference, c.First, c.Now = builderToday, builderToday, builderToday, builderNow
		if _, err := NewRepeatingTaskCreate(NewUUID(), c); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// The rule of a recurring template without deadlines changes as the app
// writes it (captured 2026-10-22): a new rule with the first occurrence in
// tir and icsd (commits 254 and 255), or the end alone, the rule only
// (commit 249).
func TestTaskUpdateRepeatEvery(t *testing.T) {
	t.Parallel()

	first := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	rule := RepeatingRule{Unit: FrequencyUnitWeekly, Amplitude: 1, Weekdays: []time.Weekday{time.Thursday}, Anchor: builderToday, Reference: builderToday}
	env := checkWritable(t, NewTaskUpdate(NewUUID(), builderNow).RepeatEvery(rule, &first))
	if keys := payloadKeys(env); !reflect.DeepEqual(keys, []string{"icsd", "md", "rr", "tir"}) {
		t.Errorf("keys %v", keys)
	}
	end := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	rule.End = &end
	env = checkWritable(t, NewTaskUpdate(NewUUID(), builderNow).RepeatEvery(rule, nil))
	if keys := payloadKeys(env); !reflect.DeepEqual(keys, []string{"md", "rr"}) {
		t.Errorf("keys %v", keys)
	}
	if ed := env["p"].(map[string]any)["rr"].(map[string]any)["ed"]; ed != float64(end.Unix()) {
		t.Errorf("ed %v", ed)
	}
}
