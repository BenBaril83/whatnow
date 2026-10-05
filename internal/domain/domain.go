// Package domain holds the types shared by the service and API layers.
package domain

import (
	"encoding/json"
	"time"
)

// Actor is who made a change.
type Actor string

const (
	ActorUser  Actor = "user"
	ActorAgent Actor = "agent"
)

func (a Actor) Valid() bool { return a == ActorUser || a == ActorAgent }

type Status string

const (
	StatusOpen    Status = "open"
	StatusDone    Status = "done"
	StatusDropped Status = "dropped"
)

func (s Status) Valid() bool { return s == StatusOpen || s == StatusDone || s == StatusDropped }

type Size string

const (
	SizeS Size = "S"
	SizeM Size = "M"
	SizeL Size = "L"
)

func (s Size) Valid() bool { return s == SizeS || s == SizeM || s == SizeL }

type Energy string

const (
	EnergyLow  Energy = "low"
	EnergyMed  Energy = "med"
	EnergyHigh Energy = "high"
)

func (e Energy) Valid() bool { return e == EnergyLow || e == EnergyMed || e == EnergyHigh }

// Date is a calendar date in YYYY-MM-DD form.
type Date string

func (d Date) Valid() bool {
	_, err := time.Parse(time.DateOnly, string(d))
	return err == nil
}

// Ref is a link or key pulled out of a task's text, such as a ticket key or a URL.
type Ref struct {
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
}

type Task struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	Notes     string     `json:"notes"`
	RawText   string     `json:"raw_text"`
	Size      *Size      `json:"size"`
	Energy    *Energy    `json:"energy"`
	Feeling   string     `json:"feeling"`
	DueOn     *Date      `json:"due_on"`
	NotBefore *Date      `json:"not_before"`
	Status    Status     `json:"status"`
	Refs      []Ref      `json:"refs"`
	CreatedBy Actor      `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at"`
}

// Blocker says TaskID cannot start until BlockedByID is finished.
type Blocker struct {
	TaskID      int64 `json:"task_id"`
	BlockedByID int64 `json:"blocked_by_id"`
}

// Event is anything at a fixed time.
type Event struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	RawText   string     `json:"raw_text"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at"`
}

// Activity is one recorded change. Before is null for a creation and After is null for a removal.
type Activity struct {
	ID        int64           `json:"id"`
	BatchID   int64           `json:"batch_id"`
	Action    string          `json:"action"`
	Entity    string          `json:"entity"`
	EntityID  int64           `json:"entity_id"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	CreatedAt time.Time       `json:"created_at"`
}

// Pace is how hard the user wants to push today, from 1 to 5. The labels can change; there are
// always five levels.
type Pace int

func (p Pace) Valid() bool { return p >= 1 && p <= 5 }

var paceLabels = [...]string{"running on fumes", "gentle", "steady", "full steam", "go hard"}

func (p Pace) Label() string {
	if !p.Valid() {
		return ""
	}
	return paceLabels[p-1]
}

type Day struct {
	ID        int64      `json:"id"`
	Date      Date       `json:"date"`
	Pace      Pace       `json:"pace"`
	MoodNote  string     `json:"mood_note"`
	StartedAt time.Time  `json:"started_at"`
	WrappedAt *time.Time `json:"wrapped_at"`
	// TomorrowNote is what the user left for the next day when wrapping up.
	TomorrowNote string `json:"tomorrow_note"`
	// Summary is the light model's short account of the day, written when it's wrapped.
	Summary string `json:"summary"`
}

type Tier string

const (
	TierMust   Tier = "must"
	TierShould Tier = "should"
	TierCould  Tier = "could"
)

func (t Tier) Valid() bool { return t == TierMust || t == TierShould || t == TierCould }

// Trigger is what caused a plan to be made.
type Trigger string

const (
	TriggerMorning Trigger = "morning"
	TriggerPace    Trigger = "pace"
	TriggerEdit    Trigger = "edit"
	TriggerAgent   Trigger = "agent"
)

func (t Trigger) Valid() bool {
	return t == TriggerMorning || t == TriggerPace || t == TriggerEdit || t == TriggerAgent
}

type PlanEntry struct {
	TaskID    int64  `json:"task_id"`
	Tier      Tier   `json:"tier"`
	Rationale string `json:"rationale"`
}

// Plan is one version of a day's plan. Entries are in order within each tier. Fallback means
// code built the plan because the model couldn't.
type Plan struct {
	ID        int64       `json:"id"`
	DayID     int64       `json:"day_id"`
	Version   int         `json:"version"`
	Trigger   Trigger     `json:"trigger"`
	Reason    string      `json:"reason"`
	Opening   string      `json:"opening"`
	Entries   []PlanEntry `json:"entries"`
	Fallback  bool        `json:"fallback"`
	CreatedAt time.Time   `json:"created_at"`
}

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ToolUse is one tool the agent ran in a reply, shown as a chip. Error is set when it failed.
type ToolUse struct {
	Tool  string `json:"tool"`
	Label string `json:"label"`
	Error string `json:"error"`
}

// Message is one chat message. BatchID is the reply's changes, or 0 when it changed nothing. Error
// is set on a reply the provider couldn't give, and Text then says so in plain words.
type Message struct {
	ID        int64     `json:"id"`
	Date      Date      `json:"date"`
	Role      Role      `json:"role"`
	Text      string    `json:"text"`
	Tools     []ToolUse `json:"tools"`
	BatchID   int64     `json:"batch_id"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
}

// ChatFold is the running summary of a date's older chat. Messages up to ThroughID are in it.
type ChatFold struct {
	Summary   string `json:"summary"`
	ThroughID int64  `json:"through_id"`
}
