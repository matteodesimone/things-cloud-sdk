package thingscloud

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"time"
)

// The builders below produce the write envelopes of the verified Task7 scope
// (docs/task7-write-policy.md), for callers that are not the CLI: a task,
// project or area creation, a checklist item, and sparse task updates.
// Every builder output passes the same policy History.Write applies.

// writeItem is one envelope of a commit: its identifier, action, kind and
// payload.
type writeItem struct {
	id      string
	action  ItemAction
	kind    ItemKind
	payload any
}

func (w writeItem) UUID() string { return w.id }

func (w writeItem) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		T ItemAction `json:"t"`
		E ItemKind   `json:"e"`
		P any        `json:"p"`
	}{w.action, w.kind, w.payload})
}

// TaskCreate describes a new task or project. Headings are outside it: their
// relationship to the tasks under them is not verified for writers.
type TaskCreate struct {
	Title string
	// Type is TaskTypeTask or TaskTypeProject.
	Type TaskType
	Note string
	// At most one container: an area, or a project.
	AreaID    string
	ProjectID string
	TagIDs    []string
	// Schedule is the start state when ScheduledDate is nil: Inbox, Anytime
	// or Someday. A task in a container is never in the Inbox: an Inbox
	// schedule (the zero value) becomes Anytime there. A project is always
	// Anytime, without a start date.
	Schedule TaskSchedule
	// ScheduledDate is the day the task starts; the task goes to Today when
	// the day is not after Today, to Upcoming otherwise.
	ScheduledDate *time.Time
	DeadlineDate  *time.Time
	// Now is the creation time, Today the reference day for ScheduledDate.
	Now, Today time.Time
}

// NewTaskCreate builds the complete Task7 creation envelope Things.app
// writes, and checks it against the write policy.
func NewTaskCreate(uuid string, c TaskCreate) (Identifiable, error) {
	if c.Type != TaskTypeTask && c.Type != TaskTypeProject {
		return nil, fmt.Errorf("thingscloud: can only create tasks and projects")
	}
	if c.AreaID != "" && c.ProjectID != "" {
		return nil, fmt.Errorf("thingscloud: a task goes in an area or in a project, not both")
	}
	if c.Type == TaskTypeProject {
		if c.ScheduledDate != nil || c.Schedule == TaskScheduleSomeday {
			return nil, fmt.Errorf("thingscloud: a project is created Anytime, without a start date")
		}
		c.Schedule = TaskScheduleAnytime
	}
	if (c.AreaID != "" || c.ProjectID != "") && c.Schedule == TaskScheduleInbox {
		c.Schedule = TaskScheduleAnytime
	}
	st, sr := c.Schedule, any(nil)
	if c.ScheduledDate != nil {
		day := dayStamp(*c.ScheduledDate)
		st, sr = scheduleForDay(day, c.Today), day
	}
	var dd any
	if c.DeadlineDate != nil {
		dd = dayStamp(*c.DeadlineDate)
	}
	tags := c.TagIDs
	if tags == nil {
		tags = []string{}
	}
	payload := map[string]any{
		"tp": c.Type, "sr": sr, "dds": nil, "rt": []string{}, "rmd": nil, "ss": 0,
		"tr": false, "dl": []string{}, "icp": false, "st": st, "ar": oneOrNone(c.AreaID),
		"tt": c.Title, "do": 0, "lai": nil, "tir": sr, "tg": tags, "agr": []string{},
		"ix": 0, "cd": stamp(c.Now), "lt": false, "icc": 0, "md": nil, "ti": 0, "dd": dd,
		"ato": nil, "nt": fullTextNote(c.Note), "icsd": nil, "pr": oneOrNone(c.ProjectID),
		"rp": nil, "acrd": nil, "sp": nil, "sb": 0, "rr": nil, "xx": defaultExtensionData(),
	}
	item := writeItem{id: uuid, action: ItemActionCreated, kind: ItemKindTask7, payload: payload}
	if err := checkWriteItem(item); err != nil {
		return nil, err
	}
	return item, nil
}

// NewAreaCreate builds an Area3 creation.
func NewAreaCreate(uuid, title string) Identifiable {
	return writeItem{id: uuid, action: ItemActionCreated, kind: ItemKindArea3, payload: map[string]any{
		"tt": title, "ix": 0, "tg": []string{}, "xx": defaultExtensionData(),
	}}
}

// NewChecklistItemCreate builds a ChecklistItem3 creation: an open step of
// the task, at position ix.
func NewChecklistItemCreate(uuid, taskID, title string, ix int, now time.Time) Identifiable {
	return writeItem{id: uuid, action: ItemActionCreated, kind: ItemKindChecklistItem3, payload: map[string]any{
		"cd": stamp(now), "md": nil, "tt": title, "ss": 0, "sp": nil, "ix": ix,
		"ts": []string{taskID}, "lt": false, "xx": defaultExtensionData(),
	}}
}

// TaskUpdate is a sparse Task7 modification: only the properties set on it
// are written, with the modification date.
type TaskUpdate struct {
	id     string
	fields map[string]any
}

// NewTaskUpdate starts a modification of the task uuid at time now.
func NewTaskUpdate(uuid string, now time.Time) *TaskUpdate {
	return &TaskUpdate{id: uuid, fields: map[string]any{"md": stamp(now)}}
}

func (u *TaskUpdate) UUID() string { return u.id }

func (u *TaskUpdate) MarshalJSON() ([]byte, error) {
	return writeItem{id: u.id, action: ItemActionModified, kind: ItemKindTask7, payload: u.fields}.MarshalJSON()
}

// Title replaces the title.
func (u *TaskUpdate) Title(title string) *TaskUpdate { u.fields["tt"] = title; return u }

// Note replaces the whole note; an empty text clears it.
func (u *TaskUpdate) Note(text string) *TaskUpdate { u.fields["nt"] = fullTextNote(text); return u }

// Schedule sets the start state without a date: Inbox, Anytime or Someday.
func (u *TaskUpdate) Schedule(st TaskSchedule) *TaskUpdate {
	u.fields["st"], u.fields["sr"], u.fields["tir"] = st, nil, nil
	return u
}

// ScheduleDate sets the day the task starts: Today when the day is not after
// today, Upcoming otherwise.
func (u *TaskUpdate) ScheduleDate(day, today time.Time) *TaskUpdate {
	d := dayStamp(day)
	u.fields["st"], u.fields["sr"], u.fields["tir"] = scheduleForDay(d, today), d, d
	return u
}

// Deadline sets the deadline day, or clears it when day is nil. A new day
// also clears the suppression from Today (dds), as the app does: a task
// taken out of Today while due comes back with its new deadline.
func (u *TaskUpdate) Deadline(day *time.Time) *TaskUpdate {
	if day == nil {
		u.fields["dd"] = nil
	} else {
		u.fields["dd"], u.fields["dds"] = dayStamp(*day), nil
	}
	return u
}

// Complete marks the task completed at the given time.
func (u *TaskUpdate) Complete(at time.Time) *TaskUpdate {
	u.fields["ss"], u.fields["sp"] = TaskStatusCompleted, stamp(at)
	return u
}

// Cancel marks the task canceled at the given time, as «Mark as
// canceled» in the app does: the completion with the status canceled.
func (u *TaskUpdate) Cancel(at time.Time) *TaskUpdate {
	u.fields["ss"], u.fields["sp"] = TaskStatusCanceled, stamp(at)
	return u
}

// Reminder sets the reminder of the task, the time of day on its start
// date as an offset from midnight in the local zone (Things writes it in
// seconds), or clears it when at is nil. Things wants a start date for a
// reminder: the caller sets one in the same update when the task has none.
func (u *TaskUpdate) Reminder(at *time.Duration) *TaskUpdate {
	if at == nil {
		u.fields["ato"] = nil
	} else {
		u.fields["ato"] = int64(at.Seconds())
	}
	return u
}

// Reopen marks the task open again, clearing its completion date.
func (u *TaskUpdate) Reopen() *TaskUpdate {
	u.fields["ss"], u.fields["sp"] = TaskStatusPending, nil
	return u
}

// Trash moves the task to the trash, or restores it.
func (u *TaskUpdate) Trash(trashed bool) *TaskUpdate { u.fields["tr"] = trashed; return u }

// MoveToArea puts the task directly in an area. Only the containers are
// written: the start state and dates stay as they are.
func (u *TaskUpdate) MoveToArea(areaID string) *TaskUpdate {
	return u.containers([]string{areaID}, []string{}, []string{})
}

// MoveToProject puts the task directly in a project, out of any heading.
// Only the containers are written.
func (u *TaskUpdate) MoveToProject(projectID string) *TaskUpdate {
	return u.containers([]string{}, []string{projectID}, []string{})
}

// MoveOut takes the task out of every area, project and heading. Only the
// containers are written.
func (u *TaskUpdate) MoveOut() *TaskUpdate {
	return u.containers([]string{}, []string{}, []string{})
}

func (u *TaskUpdate) containers(ar, pr, agr []string) *TaskUpdate {
	u.fields["ar"], u.fields["pr"], u.fields["agr"] = ar, pr, agr
	return u
}

// checkWriteItem applies the write policy to one envelope.
func checkWriteItem(item Identifiable) error {
	body, err := json.Marshal(map[string]any{item.UUID(): item})
	if err != nil {
		return err
	}
	_, err = validateTaskWrites(body)
	return err
}

func oneOrNone(id string) []string {
	if id == "" {
		return []string{}
	}
	return []string{id}
}

// stamp is a time as the fractional Unix seconds Things writes.
func stamp(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

// dayStamp is a calendar day as Things writes dates: midnight UTC of that
// year, month and day.
func dayStamp(t time.Time) int64 {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix()
}

func scheduleForDay(day int64, today time.Time) TaskSchedule {
	if day > dayStamp(today) {
		return TaskScheduleSomeday
	}
	return TaskScheduleAnytime
}

func crc32Of(s string) uint32 { return crc32.ChecksumIEEE([]byte(s)) }

func fullTextNote(text string) map[string]any {
	return map[string]any{"_t": "tx", "ch": crc32Of(text), "v": text, "t": NoteTypeFullText}
}

func defaultExtensionData() map[string]any {
	return map[string]any{"sn": map[string]any{}, "_t": "oo"}
}

// DeadlineRule is the rule of a recurring template with deadlines («Add
// deadlines», captured 2026-10-21): every Amplitude years on Month and Day,
// or every Amplitude months on Day, its instance starting Lead days before.
// Days and months count from 0, Day -1 is the last day of the month.
// Anchor (ia) is the day the periods count from, Reference (sr) the start
// of the rule: the app writes the creation day in both.
type DeadlineRule struct {
	Unit      FrequencyUnit
	Amplitude int64
	Day       int64
	Month     *int64
	Lead      int
	Anchor    time.Time
	Reference time.Time
}

// payload is the rule as the app writes it; the write policy checks it.
func (r DeadlineRule) payload() map[string]any {
	of := map[string]any{"dy": r.Day}
	if r.Month != nil {
		of["mo"] = *r.Month
	}
	return map[string]any{
		"ia": dayStamp(r.Anchor), "of": []map[string]any{of}, "tp": 0, "ts": -r.Lead, "fu": r.Unit, "rc": 0,
		"sr": dayStamp(r.Reference), "rrv": 4, "fa": r.Amplitude, "ed": noDateStamp,
	}
}

// noDateStamp is 4001-01-01, the date of «no date»: on a template, «Add
// deadlines», and the end of a rule that does not end.
const noDateStamp int64 = 64092211200

// DeadlineTemplate is a new recurring template with deadlines in a
// project: Start is the start of its next instance, which the app creates
// when that day comes.
type DeadlineTemplate struct {
	Title, Note string
	ProjectID   string
	Rule        DeadlineRule
	Start, Now  time.Time
}

// NewDeadlineTemplateCreate builds the creation of a template with
// deadlines as Things.app writes it (captured 2026-10-21, commits 97, 100
// and 103), and checks it against the write policy.
func NewDeadlineTemplateCreate(uuid string, c DeadlineTemplate) (Identifiable, error) {
	start := dayStamp(c.Start)
	payload := map[string]any{
		"tp": TaskTypeTask, "sr": nil, "dds": nil, "rt": []string{}, "rmd": nil, "ss": 0,
		// st 2, the schedule the app gives a template.
		"tr": false, "dl": []string{}, "icp": false, "st": TaskScheduleSomeday, "ar": []string{},
		"tt": c.Title, "do": 0, "lai": nil, "tir": start, "tg": []string{}, "agr": []string{},
		"ix": 0, "cd": stamp(c.Now), "lt": false, "icc": 0, "md": stamp(c.Now), "ti": 0, "dd": noDateStamp,
		"ato": nil, "nt": fullTextNote(c.Note), "icsd": start, "pr": oneOrNone(c.ProjectID),
		"rp": nil, "acrd": nil, "sp": nil, "sb": 0, "rr": c.Rule.payload(), "xx": defaultExtensionData(),
	}
	item := writeItem{id: uuid, action: ItemActionCreated, kind: ItemKindTask7, payload: payload}
	if err := checkWriteItem(item); err != nil {
		return nil, err
	}
	return item, nil
}

// Repeat changes the rule of a template with deadlines as «Reschedule»
// writes it (captured 2026-10-21, commit 100): the whole rule, and the
// start of its next instance in tir and icsd.
func (u *TaskUpdate) Repeat(rule DeadlineRule, start time.Time) *TaskUpdate {
	u.fields["rr"] = rule.payload()
	u.fields["tir"] = dayStamp(start)
	u.fields["icsd"] = dayStamp(start)
	return u
}

// RepeatingRule is the rule of a recurring template on fixed dates without
// deadlines (captured 2026-10-22): every Amplitude days; weeks, on
// Weekdays in order from Sunday; months, on Day; years, on Month and Day.
// Days and months count from 0, Day -1 is the last day of the month. End
// is the last day of the rule, included, or nil when it does not end.
// Anchor (ia) is the day the periods count from, Reference (sr) the start
// of the rule.
type RepeatingRule struct {
	Unit      FrequencyUnit
	Amplitude int64
	Weekdays  []time.Weekday
	Day       int64
	Month     *int64
	End       *time.Time
	Anchor    time.Time
	Reference time.Time
}

// payload is the rule as the app writes it; the write policy checks it.
func (r RepeatingRule) payload() map[string]any {
	var of []map[string]any
	switch r.Unit {
	case FrequencyUnitDaily:
		of = []map[string]any{{"dy": 0}}
	case FrequencyUnitWeekly:
		of = []map[string]any{}
		for _, wd := range r.Weekdays {
			of = append(of, map[string]any{"wd": int(wd)})
		}
	default:
		day := map[string]any{"dy": r.Day}
		if r.Month != nil {
			day["mo"] = *r.Month
		}
		of = []map[string]any{day}
	}
	ed := noDateStamp
	if r.End != nil {
		ed = dayStamp(*r.End)
	}
	return map[string]any{
		"ia": dayStamp(r.Anchor), "of": of, "tp": 0, "ts": 0, "fu": r.Unit, "rc": 0,
		"sr": dayStamp(r.Reference), "rrv": 4, "fa": r.Amplitude, "ed": ed,
	}
}

// RepeatingTask is a new recurring template on fixed dates without
// deadlines, in an area or in a project: First is the day of its first
// occurrence, whose instance the app creates when that day comes, and
// Reminder the time of day of the reminder of every instance, or nil.
type RepeatingTask struct {
	Title, Note       string
	AreaID, ProjectID string
	Rule              RepeatingRule
	First             time.Time
	Reminder          *time.Duration
	Now               time.Time
}

// NewRepeatingTaskCreate builds the creation of a recurring template
// without deadlines as Things.app writes it (captured 2026-10-22, commit
// 253), and checks it against the write policy.
func NewRepeatingTaskCreate(uuid string, c RepeatingTask) (Identifiable, error) {
	first := dayStamp(c.First)
	var ato any
	if c.Reminder != nil {
		ato = int64(c.Reminder.Seconds())
	}
	payload := map[string]any{
		"tp": TaskTypeTask, "sr": nil, "dds": nil, "rt": []string{}, "rmd": nil, "ss": 0,
		// st 2, the schedule the app gives a template.
		"tr": false, "dl": []string{}, "icp": false, "st": TaskScheduleSomeday, "ar": oneOrNone(c.AreaID),
		"tt": c.Title, "do": 0, "lai": nil, "tir": first, "tg": []string{}, "agr": []string{},
		"ix": 0, "cd": stamp(c.Now), "lt": false, "icc": 0, "md": stamp(c.Now), "ti": 0, "dd": nil,
		"ato": ato, "nt": fullTextNote(c.Note), "icsd": first, "pr": oneOrNone(c.ProjectID),
		"rp": nil, "acrd": nil, "sp": nil, "sb": 0, "rr": c.Rule.payload(), "xx": defaultExtensionData(),
	}
	item := writeItem{id: uuid, action: ItemActionCreated, kind: ItemKindTask7, payload: payload}
	if err := checkWriteItem(item); err != nil {
		return nil, err
	}
	return item, nil
}

// RepeatEvery changes the rule of a recurring template without deadlines
// as the app writes it (captured 2026-10-22): the whole rule and, when
// first is given, the day of the first occurrence of the new rule in tir
// and icsd (commits 254, 255); without it the rule alone, as when only the
// end changes (commit 249).
func (u *TaskUpdate) RepeatEvery(rule RepeatingRule, first *time.Time) *TaskUpdate {
	u.fields["rr"] = rule.payload()
	if first != nil {
		u.fields["tir"] = dayStamp(*first)
		u.fields["icsd"] = dayStamp(*first)
	}
	return u
}
