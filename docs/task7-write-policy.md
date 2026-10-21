# Task7 write support

Task6 and Task7 are versions of history-event payloads. The same task UUID can
have a Task7 creation followed by a Task6 modification. Migrating a writer
changes the envelopes it sends; it does not require rewriting an account's
existing tasks or history.

## Supported scope

The CLI sends Task7 for supported task, project, and heading creation and task
modification operations. Tag4, Area3, ChecklistItem3, and Tombstone2 operations
keep their existing formats. The SDK also accepts explicit `ItemKindTask7`
envelopes within the validated scope.

For compatibility, `ItemKindTask` remains Task6. Existing SDK callers continue
to emit the kind they requested. The readers retain support for both versions;
do not change their shared Task6 discriminator to migrate a writer.

- Creation uses the verified complete payload, including null modification
  date and recurrence configuration, and empty recurrence links.
- Project and heading creation is limited to Anytime in this first scope;
  ordinary task creation supports the verified Inbox/Anytime/Someday and
  corresponding date-based scheduling encodings.
- Updates are sparse: omitted properties remain omitted. Supported operations
  include full-note replacement, ordinary scheduling, relationships, tags,
  completion/reopening, and recoverable trash/restoration.
- Direct note-delta writes, recurrence configuration, unsupported properties,
  and Task7 permanent-deletion events are outside this first write contract.

Area and project moves explicitly clear incompatible parent relationships.
An edit cannot combine area with project or heading; project plus heading is
supported. This avoids silently choosing between conflicting destinations or
leaving a task attached to its previous container.

Task7 modifications require an existing, nonrecurring target, with one
exception: an instance of a recurring template with deadlines («Add
deadlines», `dd` at 4001-01-01 on the template) takes the writes the app
makes on it to complete it (`ss` 3), cancel it (`ss` 2) or move its start
date (`st`, `sr`, `tir`), with `md` and `sp` and nothing else (captured
2026-10-21). The template and every other instance stay refused. Before posting,
`History.Write` reads raw history from index zero and classifies the targeted
UUIDs. A single scan covers every Task7 update in the batch. This avoids
relying on the public `Task` projection, which does not retain every recurrence
field. Unknown/missing targets and recurring templates or instances fail
before any operation in that write request is posted.

This also rejects older or sparse histories without explicit values for all
three recurrence markers (`rr`, `rp`, `rt`), including tasks created by the
legacy sparse SDK example. Such a task may be ordinary, but the new checker
does not infer that from incomplete evidence. CLI updates therefore have a
compatibility limitation for these records; there is no automatic Task6
fallback. Existing explicit Task6 SDK calls keep their previous behavior.

This preflight adds network reads and work proportional to the history size
for update requests. It uses a fixed checked history head as the commit
ancestor; it does not silently retry a failed commit. Existing Task6 writes
keep their prior behavior and do not gain this recurrence protection.

A controlled stale-ancestor test wrote one title update, then submitted a
second update to the same UUID and field using the preceding head. The server
returned HTTP 409 for the second request and history contained only the first
event. This verifies rejection in that case; broader concurrent/offline
convergence remains outside the tested scope.

## Explicit SDK update

```go
update := things.TaskActionItem{
    Item: things.Item{
        UUID: taskUUID,
        Kind: things.ItemKindTask7,
        Action: things.ItemActionModified,
    },
    P: things.TaskActionItemPayload{
        Title: things.String("Updated title"),
        ModificationDate: things.Time(time.Now()),
    },
}
if err := history.Write(update); err != nil {
    return err
}
```

Do not serialize a projected task back as a complete replacement: unmodeled
properties such as modern repeater data cannot be reconstructed from it.
Use the CLI's complete creation payload or an equivalent validated full
envelope for Task7 creation; its required nulls differ from the legacy SDK's
sparse `TaskActionItemPayload` encoding.

## Mixed-version evidence

In the disposable account, the current Task6 CLI performed a title-only edit
on an ordinary Task7 task and on a native Task7 repeating template. Both writes
were persisted as Task6 modifications containing only `tt` and `md`, on the
original UUIDs. Things applied the titles and preserved the checked notes,
scheduling, status, and recurrence columns. The template's stored recurrence
rule remained byte-identical.

The native weekly-after-completion control used a non-null `rr` object with
`rrv=4`, `tp=1`, `fu=256`, and `fa=1`; `rp` was null. The app created a template
and a separate instance linked through `rt`. Completing the instance in Things
emitted a Task7 `ss`/`sp`/`md` update on the instance and an `acrd`/`tir` update
on its template. The app then showed September 13 as the next occurrence.

That confirms title-only mixed-version compatibility for these cases. It
does not establish recurring lifecycle support: changing an envelope's kind
does not synthesize the companion template updates. Non-null modern `rp`,
other recurrence modes, and concurrent/offline convergence remain unverified.

See [the live verification report](task7-write-verification.md) for the tested
ordinary-operation matrix and its limits.

## Builders for SDK callers

`task_builders.go` exposes the shapes above to callers that are not the CLI:
`NewTaskCreate` (tasks and projects, with the complete creation payload),
`NewAreaCreate`, `NewChecklistItemCreate` and the sparse `TaskUpdate`. Each
builder output passes the same policy `History.Write` applies.

`TaskUpdate.MoveToArea`, `MoveToProject` and `MoveOut` write only the
containers (`ar`, `pr`, `agr`) and the modification date: unlike the CLI's
`move-to-area` and `move-to-project`, which also reset the task to Anytime,
they keep its start state and dates. The policy admits this sparse shape,
since `st`, `sr` and `tir` are only required together when one of them is
written. The verification above moved tasks between containers with
explicit empty relationship arrays; a container-only move of a task that
has a start date is still to be verified live. A task leaving the Inbox
needs `st` too: callers add `Schedule(TaskScheduleAnytime)`, which loses
nothing, as an Inbox task has no start date.

## Templates with deadlines

A recurring template with deadlines («Add deadlines», `dd` at 4001-01-01)
is written in the shapes Things.app writes (captured 2026-10-21 on a test
account):

- **Creation** (`NewDeadlineTemplateCreate`): the complete Task7 payload of
  a task in one project, with `st` 2, `sr` null, `md` at the creation time,
  `tir` and `icsd` both at the start of the next instance, `icc` 0, and a
  rule `rr` that is yearly on a day of a month (`fu` 4, `of: [{dy, mo}]`) or
  monthly on a day (`fu` 8, `of: [{dy}]`), every `fa` periods, fixed
  (`tp` 0), without end (`ed` at 4001-01-01, `rc` 0, `rrv` 4), its instance
  `-ts` days before. Any other rule is refused.
- **Modifications** on such a template only: title and note (`tt`, `nt`);
  the rule with the start of the next instance (`TaskUpdate.Repeat`: `rr`,
  `tir` and `icsd` together, without `st` and `sr`); the trash (`tr`). Each
  with `md`. Completing it, a deadline or a start date on it, and these
  writes on a template without deadlines or on a plain task are refused.

The instances are created by the app when their start comes, together with
the update of the template; writers do not create them. Trashing a
template leaves its open instance as it is.
