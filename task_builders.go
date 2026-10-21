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
