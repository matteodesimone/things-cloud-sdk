package thingscloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

type task7WritePlan struct {
	modificationTargets map[string]struct{}
	// modificationKeys are the properties each modification writes: an
	// instance of a template with deadlines takes only some of them.
	modificationKeys map[string][]string
	// modificationRules are the rules the modifications write: a template
	// takes only a rule of its kind.
	modificationRules map[string]json.RawMessage
	// modificationStatus is the status a modification writes (ss), when it
	// writes one: the companion of a template after completion goes with a
	// completion only.
	modificationStatus map[string]int64
	// modificationTrash are the modifications that put a task in the trash.
	modificationTrash map[string]bool
	creationTargets   map[string]struct{}
	tombstoneTargets  map[string]struct{}
}

func (p *task7WritePlan) needsPreflight() bool { return len(p.modificationTargets) != 0 }

func validateTaskWrites(body []byte) (*task7WritePlan, error) {
	var envelopes map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelopes); err != nil {
		return nil, fmt.Errorf("refusing to write an invalid item envelope")
	}
	plan := &task7WritePlan{
		modificationTargets: make(map[string]struct{}),
		modificationKeys:    make(map[string][]string),
		modificationRules:   make(map[string]json.RawMessage),
		modificationStatus:  make(map[string]int64),
		modificationTrash:   make(map[string]bool),
		creationTargets:     make(map[string]struct{}),
		tombstoneTargets:    make(map[string]struct{}),
	}
	type tombstoneEnvelope struct {
		kind string
		raw  json.RawMessage
	}
	var tombstones []tombstoneEnvelope
	for id, raw := range envelopes {
		if err := rejectDuplicateJSONKeys(raw, false); err != nil {
			return nil, fmt.Errorf("refusing to write an invalid item envelope")
		}
		var legacyEnvelope struct {
			Kind ItemKind `json:"e"`
		}
		if err := json.Unmarshal(raw, &legacyEnvelope); err != nil {
			return nil, fmt.Errorf("refusing to write an invalid item envelope")
		}
		kind := string(legacyEnvelope.Kind)
		switch kind {
		case "Task7":
			if !utf8.Valid(raw) || rejectUnpairedJSONSurrogates(raw) != nil {
				return nil, fmt.Errorf("refusing to write invalid Task7 envelope")
			}
			if err := rejectDuplicateJSONKeys(raw, true); err != nil {
				return nil, fmt.Errorf("refusing to write invalid Task7 envelope")
			}
			fields, err := jsonObject(raw)
			if err != nil {
				return nil, fmt.Errorf("refusing to write invalid Task7 envelope")
			}
			action, ok := jsonInteger(fields["t"])
			if !ok || len(fields) != 3 {
				return nil, fmt.Errorf("refusing to write invalid Task7 envelope")
			}
			payload, err := jsonObject(fields["p"])
			if err != nil {
				return nil, fmt.Errorf("refusing to write invalid Task7 payload")
			}
			switch action {
			case int64(ItemActionCreated):
				err = validateTask7Create(payload)
				if err == nil {
					plan.creationTargets[id] = struct{}{}
				}
			case int64(ItemActionModified):
				err = validateTask7Modification(payload)
				if err == nil {
					plan.modificationTargets[id] = struct{}{}
					for key := range payload {
						plan.modificationKeys[id] = append(plan.modificationKeys[id], key)
					}
					if rule, ok := payload["rr"]; ok {
						plan.modificationRules[id] = rule
					}
					if ss, ok := jsonInteger(payload["ss"]); ok {
						plan.modificationStatus[id] = ss
					}
					if tr, ok := jsonBool(payload["tr"]); ok && tr {
						plan.modificationTrash[id] = true
					}
				}
			default:
				err = fmt.Errorf("Task7 action %d is outside the verified write scope", action)
			}
			if err != nil {
				return nil, fmt.Errorf("refusing to write Task7: %w", err)
			}
		case "Task6", "Task4", "Task3", "Task":
			// Preserve existing write kinds and serialization behavior.
		case "Tombstone2", "Tombstone":
			tombstones = append(tombstones, tombstoneEnvelope{kind: kind, raw: raw})
		default:
			if strings.HasPrefix(kind, "Task") {
				return nil, fmt.Errorf("refusing to write unverified task kind %s", taskKindDiagnostic(ItemKind(kind)))
			}
		}
	}
	if len(plan.creationTargets)+len(plan.modificationTargets) != 0 {
		for _, tombstone := range tombstones {
			target, err := validateTask7CompanionTombstone(tombstone.raw, tombstone.kind)
			if err != nil {
				return nil, fmt.Errorf("refusing to write Task7 batch: invalid tombstone companion")
			}
			plan.tombstoneTargets[target] = struct{}{}
		}
	}
	for _, targets := range []map[string]struct{}{plan.creationTargets, plan.modificationTargets} {
		for target := range targets {
			if _, ambiguous := plan.tombstoneTargets[target]; ambiguous {
				return nil, fmt.Errorf("refusing to write Task7: target %s is both changed and deleted in one commit", target)
			}
		}
	}
	return plan, nil
}

func validateTask7CompanionTombstone(raw json.RawMessage, kind string) (string, error) {
	if !utf8.Valid(raw) || rejectUnpairedJSONSurrogates(raw) != nil || rejectDuplicateJSONKeys(raw, true) != nil {
		return "", fmt.Errorf("invalid tombstone envelope")
	}
	fields, err := jsonObject(raw)
	if err != nil || exactKeys(fields, map[string]struct{}{"e": {}, "t": {}, "p": {}}) != nil {
		return "", fmt.Errorf("invalid tombstone envelope")
	}
	action, ok := jsonInteger(fields["t"])
	if !ok || action != 0 {
		return "", fmt.Errorf("invalid tombstone action")
	}
	payload, err := jsonObject(fields["p"])
	if err != nil || exactKeys(payload, map[string]struct{}{"dloid": {}, "dld": {}}) != nil {
		return "", fmt.Errorf("invalid tombstone payload")
	}
	target, ok := jsonString(payload["dloid"])
	if !ok || target == "" || requiredTimestamp(payload["dld"], true) != nil {
		return "", fmt.Errorf("invalid tombstone payload")
	}
	if kind == "Tombstone2" {
		if ValidateUUID(target) != nil {
			return "", fmt.Errorf("invalid tombstone target")
		}
	} else if ValidateUUID(target) != nil {
		target = EncodeLegacyIdentifier(target)
	}
	return target, nil
}

var task7CreateKeys = map[string]struct{}{
	"tp": {}, "sr": {}, "dds": {}, "rt": {}, "rmd": {}, "ss": {}, "tr": {}, "dl": {},
	"icp": {}, "st": {}, "ar": {}, "tt": {}, "do": {}, "lai": {}, "tir": {}, "tg": {},
	"agr": {}, "ix": {}, "cd": {}, "lt": {}, "icc": {}, "md": {}, "ti": {}, "dd": {},
	"ato": {}, "nt": {}, "icsd": {}, "pr": {}, "rp": {}, "acrd": {}, "sp": {}, "sb": {},
	"rr": {}, "xx": {},
}

func validateTask7Create(p map[string]json.RawMessage) error {
	if err := exactKeys(p, task7CreateKeys); err != nil {
		return fmt.Errorf("create payload %w", err)
	}
	if !jsonNull(p["rr"]) {
		if jsonNull(p["dd"]) {
			return validateRepeatingTaskCreate(p)
		}
		return validateDeadlineTemplateCreate(p)
	}
	tp, ok := jsonInteger(p["tp"])
	if !ok || tp < 0 || tp > 2 {
		return fmt.Errorf("create tp must be 0, 1, or 2")
	}
	st, ok := jsonInteger(p["st"])
	if !ok || st < 0 || st > 2 || (tp != 0 && st != 1) {
		return fmt.Errorf("create st is invalid for task type")
	}
	if ss, ok := jsonInteger(p["ss"]); !ok || ss != 0 {
		return fmt.Errorf("create ss must be 0")
	}
	if tr, ok := jsonBool(p["tr"]); !ok || tr {
		return fmt.Errorf("create tr must be false")
	}
	for _, key := range []string{"icp", "lt"} {
		if value, ok := jsonBool(p[key]); !ok || value {
			return fmt.Errorf("create %s must be false", key)
		}
	}
	for _, key := range []string{"do", "ix", "icc", "ti", "sb"} {
		if value, ok := jsonInteger(p[key]); !ok || value != 0 {
			return fmt.Errorf("create %s must be 0", key)
		}
	}
	if _, ok := jsonString(p["tt"]); !ok {
		return fmt.Errorf("create tt must be a string")
	}
	if err := requiredTimestamp(p["cd"], true); err != nil {
		return fmt.Errorf("create cd %w", err)
	}
	for _, key := range []string{"sr", "tir", "dd"} {
		if err := nullableTimestamp(p[key]); err != nil {
			return fmt.Errorf("create %s %w", key, err)
		}
	}
	srNull, tirNull := jsonNull(p["sr"]), jsonNull(p["tir"])
	if srNull != tirNull {
		return fmt.Errorf("create sr and tir must have the same date")
	}
	if st == 0 && !srNull {
		return fmt.Errorf("create st 0 requires null sr and tir")
	}
	if !srNull {
		var sr, tir float64
		_ = json.Unmarshal(p["sr"], &sr)
		_ = json.Unmarshal(p["tir"], &tir)
		if sr != tir {
			return fmt.Errorf("create sr and tir must have the same date")
		}
	}
	for _, key := range []string{"dds", "rmd", "lai", "md", "ato", "icsd", "rp", "acrd", "sp", "rr"} {
		if !jsonNull(p[key]) {
			return fmt.Errorf("create %s must be null", key)
		}
	}
	for _, key := range []string{"rt", "dl"} {
		values, err := jsonStringArray(p[key])
		if err != nil || len(values) != 0 {
			return fmt.Errorf("create %s must be an empty array", key)
		}
	}
	for _, key := range []string{"ar", "pr", "agr", "tg"} {
		if err := validateReferenceArray(p[key]); err != nil {
			return fmt.Errorf("create %s %w", key, err)
		}
		if key != "tg" {
			values, _ := jsonStringArray(p[key])
			if len(values) > 1 {
				return fmt.Errorf("create %s supports at most one parent identifier", key)
			}
		}
	}
	if err := validateFullTextNote(p["nt"]); err != nil {
		return fmt.Errorf("create nt %w", err)
	}
	if err := validateDefaultExtension(p["xx"]); err != nil {
		return fmt.Errorf("create xx %w", err)
	}
	return nil
}

var task7ModificationKeys = map[string]struct{}{
	"md": {}, "tt": {}, "nt": {}, "st": {}, "sr": {}, "tir": {}, "dd": {}, "ar": {},
	"pr": {}, "agr": {}, "tg": {}, "ss": {}, "sp": {}, "tr": {}, "ix": {}, "ti": {}, "do": {},
	"dds": {}, "ato": {}, "rr": {}, "icsd": {}, "acrd": {},
}

// validateDeadlineTemplateCreate checks the creation of a recurring
// template with deadlines, as the app writes it (captured 2026-10-21): a
// task in a project, scheduled (st 2) without a start date, the start of
// its next instance in tir and icsd, «Add deadlines» in dd, md at the
// creation time, and a rule of the scadenzario.
func validateDeadlineTemplateCreate(p map[string]json.RawMessage) error {
	ints := map[string]int64{"tp": 0, "st": 2, "ss": 0, "do": 0, "ix": 0, "icc": 0, "ti": 0, "sb": 0, "dd": noDateStamp}
	for key, want := range ints {
		if value, ok := jsonInteger(p[key]); !ok || value != want {
			return fmt.Errorf("template create %s must be %d", key, want)
		}
	}
	for _, key := range []string{"tr", "icp", "lt"} {
		if value, ok := jsonBool(p[key]); !ok || value {
			return fmt.Errorf("template create %s must be false", key)
		}
	}
	if _, ok := jsonString(p["tt"]); !ok {
		return fmt.Errorf("template create tt must be a string")
	}
	for _, key := range []string{"cd", "md"} {
		if err := requiredTimestamp(p[key], true); err != nil {
			return fmt.Errorf("template create %s %w", key, err)
		}
	}
	if err := sameStartAndCheck(p["tir"], p["icsd"]); err != nil {
		return fmt.Errorf("template create %w", err)
	}
	for _, key := range []string{"sr", "dds", "rmd", "lai", "ato", "rp", "acrd", "sp"} {
		if !jsonNull(p[key]) {
			return fmt.Errorf("template create %s must be null", key)
		}
	}
	for _, key := range []string{"rt", "dl", "ar", "agr"} {
		values, err := jsonStringArray(p[key])
		if err != nil || len(values) != 0 {
			return fmt.Errorf("template create %s must be an empty array", key)
		}
	}
	if err := validateReferenceArray(p["pr"]); err != nil {
		return fmt.Errorf("template create pr %w", err)
	}
	if values, _ := jsonStringArray(p["pr"]); len(values) != 1 {
		return fmt.Errorf("template create pr must name one project")
	}
	if err := validateReferenceArray(p["tg"]); err != nil {
		return fmt.Errorf("template create tg %w", err)
	}
	if err := validateFullTextNote(p["nt"]); err != nil {
		return fmt.Errorf("template create nt %w", err)
	}
	if err := validateDefaultExtension(p["xx"]); err != nil {
		return fmt.Errorf("template create xx %w", err)
	}
	return validateDeadlineRule(p["rr"])
}

// sameStartAndCheck says tir and icsd are the same day: the start of the
// next instance, and the day the app looks whether to create it.
func sameStartAndCheck(tir, icsd json.RawMessage) error {
	a, okA := jsonInteger(tir)
	b, okB := jsonInteger(icsd)
	if !okA || !okB || a <= 0 || a != b {
		return fmt.Errorf("tir and icsd must be the same day")
	}
	return nil
}

// validateRepeatingTaskCreate checks the creation of a recurring template
// on fixed dates without deadlines, as the app writes it (captured
// 2026-10-22, commit 253): a task in an area or in a project, scheduled
// (st 2) without a start date or a deadline, its first occurrence in tir
// and icsd, a reminder or none, md at the creation time, and a rule on
// fixed dates that does not start in advance.
func validateRepeatingTaskCreate(p map[string]json.RawMessage) error {
	ints := map[string]int64{"tp": 0, "st": 2, "ss": 0, "do": 0, "ix": 0, "icc": 0, "ti": 0, "sb": 0}
	for key, want := range ints {
		if value, ok := jsonInteger(p[key]); !ok || value != want {
			return fmt.Errorf("repeating create %s must be %d", key, want)
		}
	}
	for _, key := range []string{"tr", "icp", "lt"} {
		if value, ok := jsonBool(p[key]); !ok || value {
			return fmt.Errorf("repeating create %s must be false", key)
		}
	}
	if _, ok := jsonString(p["tt"]); !ok {
		return fmt.Errorf("repeating create tt must be a string")
	}
	for _, key := range []string{"cd", "md"} {
		if err := requiredTimestamp(p[key], true); err != nil {
			return fmt.Errorf("repeating create %s %w", key, err)
		}
	}
	if err := sameStartAndCheck(p["tir"], p["icsd"]); err != nil {
		return fmt.Errorf("repeating create %w", err)
	}
	for _, key := range []string{"sr", "dd", "dds", "rmd", "lai", "rp", "acrd", "sp"} {
		if !jsonNull(p[key]) {
			return fmt.Errorf("repeating create %s must be null", key)
		}
	}
	if err := validateAlarm(p["ato"]); err != nil {
		return fmt.Errorf("repeating create %w", err)
	}
	for _, key := range []string{"rt", "dl", "agr"} {
		values, err := jsonStringArray(p[key])
		if err != nil || len(values) != 0 {
			return fmt.Errorf("repeating create %s must be an empty array", key)
		}
	}
	places := 0
	for _, key := range []string{"ar", "pr"} {
		if err := validateReferenceArray(p[key]); err != nil {
			return fmt.Errorf("repeating create %s %w", key, err)
		}
		values, _ := jsonStringArray(p[key])
		if len(values) > 1 {
			return fmt.Errorf("repeating create %s supports at most one parent identifier", key)
		}
		places += len(values)
	}
	if places != 1 {
		return fmt.Errorf("repeating create must name one area or one project")
	}
	if err := validateReferenceArray(p["tg"]); err != nil {
		return fmt.Errorf("repeating create tg %w", err)
	}
	if err := validateFullTextNote(p["nt"]); err != nil {
		return fmt.Errorf("repeating create nt %w", err)
	}
	if err := validateDefaultExtension(p["xx"]); err != nil {
		return fmt.Errorf("repeating create xx %w", err)
	}
	return validateRepeatingRule(p["rr"])
}

// validateAlarm checks a reminder: null, or the seconds of a day.
func validateAlarm(raw json.RawMessage) error {
	if jsonNull(raw) {
		return nil
	}
	if value, ok := jsonInteger(raw); !ok || value < 0 || value >= 86400 {
		return fmt.Errorf("ato must be null or the seconds of a day")
	}
	return nil
}

// validateRepeatingRule checks the rule of a recurring template without
// deadlines as the app writes it (captured 2026-10-22): every fa days (the
// day 0), weeks (one to seven days of the week, in order from Sunday),
// months (a day) or years (a day of a month), fixed (not after
// completion), without starting in advance, ending on a day or never.
func validateRepeatingRule(raw json.RawMessage) error {
	rr, err := jsonObject(raw)
	if err != nil {
		return fmt.Errorf("rr must be an object")
	}
	if err := exactKeys(rr, deadlineRuleKeys); err != nil {
		return fmt.Errorf("rr %w", err)
	}
	for key, want := range map[string]int64{"tp": 0, "rc": 0, "rrv": 4, "ts": 0} {
		if value, ok := jsonInteger(rr[key]); !ok || value != want {
			return fmt.Errorf("rr %s must be %d", key, want)
		}
	}
	if fa, ok := jsonInteger(rr["fa"]); !ok || fa < 1 {
		return fmt.Errorf("rr fa must be 1 or more")
	}
	for _, key := range []string{"ia", "sr", "ed"} {
		if value, ok := jsonInteger(rr[key]); !ok || value <= 0 {
			return fmt.Errorf("rr %s must be a day", key)
		}
	}
	var of []map[string]json.RawMessage
	if err := json.Unmarshal(rr["of"], &of); err != nil || len(of) == 0 {
		return fmt.Errorf("rr of must hold the days")
	}
	fu, _ := jsonInteger(rr["fu"])
	switch FrequencyUnit(fu) {
	case FrequencyUnitDaily:
		if len(of) != 1 || exactKeys(of[0], map[string]struct{}{"dy": {}}) != nil {
			return fmt.Errorf("rr of of a daily rule must be the day 0")
		}
		if dy, ok := jsonInteger(of[0]["dy"]); !ok || dy != 0 {
			return fmt.Errorf("rr of of a daily rule must be the day 0")
		}
	case FrequencyUnitWeekly:
		if len(of) > 7 {
			return fmt.Errorf("rr of of a weekly rule holds at most seven days")
		}
		last := int64(-1)
		for _, day := range of {
			wd, ok := jsonInteger(day["wd"])
			if exactKeys(day, map[string]struct{}{"wd": {}}) != nil || !ok || wd <= last || wd > 6 {
				return fmt.Errorf("rr of of a weekly rule must be days of the week in order from Sunday")
			}
			last = wd
		}
	case FrequencyUnitMonthly, FrequencyUnitYearly:
		if len(of) != 1 {
			return fmt.Errorf("rr of must hold one day")
		}
		return validateDayOfRule(FrequencyUnit(fu), of[0])
	default:
		return fmt.Errorf("rr fu must be daily, weekly, monthly or yearly")
	}
	return nil
}

// validateDayOfRule checks the day of a monthly or yearly rule: a day of
// the month, and for a yearly rule its month.
func validateDayOfRule(fu FrequencyUnit, day map[string]json.RawMessage) error {
	want := map[string]struct{}{"dy": {}}
	if fu == FrequencyUnitYearly {
		want["mo"] = struct{}{}
	}
	if err := exactKeys(day, want); err != nil {
		return fmt.Errorf("rr of %w", err)
	}
	if dy, ok := jsonInteger(day["dy"]); !ok || dy < -1 || dy > 30 {
		return fmt.Errorf("rr of dy must be a day of the month")
	}
	if raw, ok := day["mo"]; ok {
		if mo, ok := jsonInteger(raw); !ok || mo < 0 || mo > 11 {
			return fmt.Errorf("rr of mo must be a month")
		}
	}
	return nil
}

var deadlineRuleKeys = map[string]struct{}{
	"ia": {}, "of": {}, "tp": {}, "ts": {}, "fu": {}, "rc": {}, "sr": {}, "rrv": {}, "fa": {}, "ed": {},
}

// validateDeadlineRule checks a rule of the scadenzario as the app writes
// it on a template with deadlines (captured 2026-10-21): yearly on a day
// of a month, or monthly on a day, every fa periods, without end, fixed
// (not after completion), its instance ts days before.
func validateDeadlineRule(raw json.RawMessage) error {
	rr, err := jsonObject(raw)
	if err != nil {
		return fmt.Errorf("rr must be an object")
	}
	if err := exactKeys(rr, deadlineRuleKeys); err != nil {
		return fmt.Errorf("rr %w", err)
	}
	fu, ok := jsonInteger(rr["fu"])
	if !ok || (fu != int64(FrequencyUnitYearly) && fu != int64(FrequencyUnitMonthly)) {
		return fmt.Errorf("rr fu must be yearly or monthly")
	}
	for key, want := range map[string]int64{"tp": 0, "rc": 0, "rrv": 4, "ed": noDateStamp} {
		if value, ok := jsonInteger(rr[key]); !ok || value != want {
			return fmt.Errorf("rr %s must be %d", key, want)
		}
	}
	if fa, ok := jsonInteger(rr["fa"]); !ok || fa < 1 {
		return fmt.Errorf("rr fa must be 1 or more")
	}
	if ts, ok := jsonInteger(rr["ts"]); !ok || ts > 0 {
		return fmt.Errorf("rr ts must be 0 or less")
	}
	for _, key := range []string{"ia", "sr"} {
		if value, ok := jsonInteger(rr[key]); !ok || value <= 0 {
			return fmt.Errorf("rr %s must be a day", key)
		}
	}
	var of []map[string]json.RawMessage
	if err := json.Unmarshal(rr["of"], &of); err != nil || len(of) != 1 {
		return fmt.Errorf("rr of must hold one day")
	}
	return validateDayOfRule(FrequencyUnit(fu), of[0])
}

func validateTask7Modification(p map[string]json.RawMessage) error {
	for key := range p {
		if _, ok := task7ModificationKeys[key]; !ok {
			return fmt.Errorf("modification contains a property outside the verified write scope")
		}
	}
	// The companion of the completion of an instance repeating after
	// completion, on its template: the start of the next instance and the
	// day of the completion, without md (captured 2026-10-22). The preflight
	// checks it goes with that completion.
	if _, ok := p["acrd"]; ok {
		if exactKeys(p, map[string]struct{}{"tir": {}, "acrd": {}}) != nil {
			return fmt.Errorf("modification acrd goes with tir alone, as the companion of a completion")
		}
		for _, key := range []string{"tir", "acrd"} {
			if value, ok := jsonInteger(p[key]); !ok || value <= 0 {
				return fmt.Errorf("modification %s must be a day", key)
			}
		}
		return nil
	}
	// The deadline suppression is only cleared, as the app does with a new
	// deadline (captured 2026-10-21); setting it is not verified.
	if raw, ok := p["dds"]; ok && !jsonNull(raw) {
		return fmt.Errorf("modification dds must be null")
	}
	if err := requiredTimestamp(p["md"], true); err != nil {
		return fmt.Errorf("modification md %w", err)
	}
	if raw, ok := p["tt"]; ok {
		if _, ok := jsonString(raw); !ok {
			return fmt.Errorf("modification tt must be a string")
		}
	}
	if raw, ok := p["nt"]; ok {
		if err := validateFullTextNote(raw); err != nil {
			return fmt.Errorf("modification nt %w", err)
		}
	}
	if raw, ok := p["st"]; ok {
		value, ok := jsonInteger(raw)
		if !ok || value < 0 || value > 2 {
			return fmt.Errorf("modification st must be 0, 1, or 2")
		}
	}
	if raw, ok := p["ss"]; ok {
		value, ok := jsonInteger(raw)
		if !ok || (value != 0 && value != 2 && value != 3) {
			return fmt.Errorf("modification ss must be 0, 2, or 3")
		}
	}
	// The reminder is the seconds of a day on the start date, as the app
	// writes it, or null (captured 2026-10-21).
	if raw, ok := p["ato"]; ok {
		if err := validateAlarm(raw); err != nil {
			return fmt.Errorf("modification %w", err)
		}
	}
	ssRaw, hasStatus := p["ss"]
	spRaw, hasStopDate := p["sp"]
	if hasStatus != hasStopDate {
		return fmt.Errorf("modification ss and sp must be written together")
	}
	if hasStatus {
		status, _ := jsonInteger(ssRaw)
		if status == 0 && !jsonNull(spRaw) {
			return fmt.Errorf("modification ss 0 requires null sp")
		}
		if status == 2 || status == 3 {
			if jsonNull(spRaw) || requiredTimestamp(spRaw, true) != nil {
				return fmt.Errorf("modification ss %d requires a positive sp timestamp", status)
			}
		}
	}
	_, hasSchedule := p["st"]
	_, hasStartDate := p["sr"]
	_, hasTodayReference := p["tir"]
	_, hasRule := p["rr"]
	_, hasCheck := p["icsd"]
	if hasRule || hasCheck {
		// The rule of a template: the whole rule, with the start of its
		// next instance in tir and icsd, without the schedule (captured
		// 2026-10-21); or, on a template without deadlines, the rule alone,
		// as when only its end changes (captured 2026-10-22). The preflight
		// checks the rule is of the template's kind.
		alone := hasRule && !hasCheck && !hasTodayReference
		if !alone && (!hasRule || !hasCheck || !hasTodayReference) || hasSchedule || hasStartDate {
			return fmt.Errorf("modification rr, tir and icsd must be written together, or rr alone, without st and sr")
		}
		if validateDeadlineRule(p["rr"]) != nil {
			if err := validateRepeatingRule(p["rr"]); err != nil {
				return fmt.Errorf("modification %w", err)
			}
		}
		if !alone {
			if err := sameStartAndCheck(p["tir"], p["icsd"]); err != nil {
				return fmt.Errorf("modification %w", err)
			}
		}
	} else if hasSchedule || hasStartDate || hasTodayReference {
		if !hasSchedule || !hasStartDate || !hasTodayReference {
			return fmt.Errorf("modification st, sr, and tir must be written together")
		}
		st, _ := jsonInteger(p["st"])
		srNull, tirNull := jsonNull(p["sr"]), jsonNull(p["tir"])
		if srNull != tirNull {
			return fmt.Errorf("modification sr and tir must have the same date")
		}
		if st == 0 && !srNull {
			return fmt.Errorf("modification st 0 requires null sr and tir")
		}
		if !srNull {
			var sr, tir float64
			_ = json.Unmarshal(p["sr"], &sr)
			_ = json.Unmarshal(p["tir"], &tir)
			if sr != tir {
				return fmt.Errorf("modification sr and tir must have the same date")
			}
		}
	}
	for _, key := range []string{"sr", "tir", "dd", "sp"} {
		if raw, ok := p[key]; ok {
			if err := nullableTimestamp(raw); err != nil {
				return fmt.Errorf("modification %s %w", key, err)
			}
		}
	}
	for _, key := range []string{"ar", "pr", "agr", "tg"} {
		if raw, ok := p[key]; ok {
			if err := validateReferenceArray(raw); err != nil {
				return fmt.Errorf("modification %s %w", key, err)
			}
		}
	}
	if raw, ok := p["tr"]; ok {
		if _, ok := jsonBool(raw); !ok {
			return fmt.Errorf("modification tr must be a boolean")
		}
	}
	for _, key := range []string{"ix", "ti", "do"} {
		if raw, ok := p[key]; ok {
			if _, ok := jsonInteger(raw); !ok {
				return fmt.Errorf("modification %s must be an integer", key)
			}
		}
	}
	return nil
}

func validateFullTextNote(raw json.RawMessage) error {
	note, err := jsonObject(raw)
	if err != nil {
		return fmt.Errorf("must be an object")
	}
	want := map[string]struct{}{"_t": {}, "ch": {}, "v": {}, "t": {}}
	if err := exactKeys(note, want); err != nil {
		return err
	}
	tag, tagOK := jsonString(note["_t"])
	typ, typeOK := jsonInteger(note["t"])
	value, valueOK := jsonString(note["v"])
	checksum, checksumOK := jsonInteger(note["ch"])
	if !tagOK || tag != "tx" || !typeOK || typ != NoteTypeFullText || !valueOK || !checksumOK || checksum < 0 || checksum > math.MaxUint32 {
		return fmt.Errorf("must be a tx full-text note with string value and CRC32 checksum")
	}
	if uint32(checksum) != crc32.ChecksumIEEE([]byte(value)) {
		return fmt.Errorf("has a checksum that does not match its full text")
	}
	return nil
}

func validateDefaultExtension(raw json.RawMessage) error {
	extension, err := jsonObject(raw)
	if err != nil {
		return fmt.Errorf("must be an object")
	}
	if err := exactKeys(extension, map[string]struct{}{"sn": {}, "_t": {}}); err != nil {
		return err
	}
	tag, ok := jsonString(extension["_t"])
	if !ok || tag != "oo" {
		return fmt.Errorf("must have _t oo")
	}
	sn, err := jsonObject(extension["sn"])
	if err != nil || len(sn) != 0 {
		return fmt.Errorf("must have an empty sn object")
	}
	return nil
}

func validateReferenceArray(raw json.RawMessage) error {
	values, err := jsonStringArray(raw)
	if err != nil {
		return fmt.Errorf("must be an array of identifiers")
	}
	for _, id := range values {
		if err := ValidateUUID(id); err != nil {
			return fmt.Errorf("contains an invalid identifier")
		}
	}
	return nil
}

func jsonObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	data := bytes.TrimSpace(raw)
	if len(data) == 0 || data[0] != '{' {
		return nil, fmt.Errorf("must be an object")
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil || result == nil {
		return nil, fmt.Errorf("must be an object")
	}
	return result, nil
}

func exactKeys(values map[string]json.RawMessage, want map[string]struct{}) error {
	if len(values) != len(want) {
		return fmt.Errorf("must contain exactly the verified properties")
	}
	for key := range values {
		if _, ok := want[key]; !ok {
			return fmt.Errorf("contains unsupported property %q", key)
		}
	}
	return nil
}

func jsonNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func jsonString(raw json.RawMessage) (string, bool) {
	data := bytes.TrimSpace(raw)
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return "", false
	}
	var value string
	if json.Unmarshal(data, &value) != nil {
		return "", false
	}
	return value, true
}

func jsonBool(raw json.RawMessage) (bool, bool) {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func jsonInteger(raw json.RawMessage) (int64, bool) {
	data := bytes.TrimSpace(raw)
	if len(data) == 0 {
		return 0, false
	}
	value, err := strconv.ParseInt(string(data), 10, 64)
	return value, err == nil
}

// rejectDuplicateJSONKeys checks the envelope object itself for every write.
// Task7 additionally checks every nested object because validation must not
// approve one interpretation while the server applies another duplicate key.
func rejectDuplicateJSONKeys(raw json.RawMessage, recursive bool) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder, recursive, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("extra JSON value")
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder, recursive bool, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if depth == 0 || recursive {
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("duplicate object key")
				}
				seen[key] = struct{}{}
			}
			if err := consumeJSONValue(decoder, recursive, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("unterminated object")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder, recursive, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("unterminated array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	return nil
}

func rejectUnpairedJSONSurrogates(raw []byte) error {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		for i++; i < len(raw) && raw[i] != '"'; i++ {
			if raw[i] != '\\' {
				continue
			}
			i++
			if i >= len(raw) || raw[i] != 'u' {
				continue
			}
			value, ok := jsonHex16(raw, i+1)
			if !ok {
				return fmt.Errorf("invalid unicode escape")
			}
			i += 4
			switch {
			case value >= 0xd800 && value <= 0xdbff:
				if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
					return fmt.Errorf("unpaired unicode surrogate")
				}
				low, ok := jsonHex16(raw, i+3)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return fmt.Errorf("unpaired unicode surrogate")
				}
				i += 6
			case value >= 0xdc00 && value <= 0xdfff:
				return fmt.Errorf("unpaired unicode surrogate")
			}
		}
	}
	return nil
}

func jsonHex16(raw []byte, start int) (uint16, bool) {
	if start+4 > len(raw) {
		return 0, false
	}
	value, err := strconv.ParseUint(string(raw[start:start+4]), 16, 16)
	return uint16(value), err == nil
}

func jsonStringArray(raw json.RawMessage) ([]string, error) {
	var values []string
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, fmt.Errorf("must be an array of strings")
	}
	return values, nil
}

const (
	minTaskTimestamp = -62135596800.0
	maxTaskTimestamp = 253402300799.0
)

func nullableTimestamp(raw json.RawMessage) error {
	if jsonNull(raw) {
		return nil
	}
	return requiredTimestamp(raw, false)
}

func requiredTimestamp(raw json.RawMessage, positive bool) error {
	var value float64
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("must be a finite timestamp")
	}
	if value < minTaskTimestamp || value > maxTaskTimestamp || (positive && value <= 0) {
		return fmt.Errorf("must be a valid timestamp")
	}
	return nil
}
