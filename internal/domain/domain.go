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
