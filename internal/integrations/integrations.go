// Package integrations pulls outside work into whatnow. Each integration declares the settings
// fields it needs and pulls a window of time; Runner keeps their settings and syncs what they pull.
package integrations

import (
	"context"
	"time"
)

type Integration interface {
	// ID names the integration in URLs, storage, and the events it pulls, such as "gcal".
	ID() string
	Name() string
	Fields() []Field
	// Pulls says whether Pull brings events, which it writes, or tickets, which the user picks from.
	Pulls() Pulls
	// Pull fetches what starts in [from, to), one Source for each part it pulls, such as a calendar.
	Pull(ctx context.Context, cfg Config, from, to time.Time) []Source
}

// Pulls is what an integration brings in.
type Pulls string

const (
	// PullsEvents writes today's events as they're pulled.
	PullsEvents Pulls = "events"
	// PullsTickets offers tickets, and only the ones the user picks become tasks, through Add.
	PullsTickets Pulls = "tickets"
)

type Kind string

const (
	// Text is kept in the database.
	Text Kind = "text"
	// Secret is kept in the secrets store and never leaves the server.
	Secret Kind = "secret"
	// List is rows of Text and Secret columns, given by Fields. Lists don't nest.
	List Kind = "list"
)

type Field struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  Kind   `json:"kind"`
	// Help is one line under the field, such as where to find a URL.
	Help string `json:"help,omitempty"`
	// Required means the integration can't pull without it. A required list needs a row, and a
	// row needs its required columns before it can be saved.
	Required bool    `json:"required"`
	Fields   []Field `json:"fields,omitempty"`
}

// Config is an integration's saved values with its secrets filled in.
type Config struct {
	Values map[string]string
	Lists  map[string][]Row
}

// Row is one row of a list. Its ID is made by the server and never changes.
type Row struct {
	ID     string            `json:"id"`
	Values map[string]string `json:"values"`
}

// Source is what one part of an integration pulled. Err is set when it couldn't be pulled, in
// words fit for the user, and never holds a secret.
type Source struct {
	Label  string
	Events []Event
	Err    error
	// Tickets is what a ticket integration pulled, and More how many matched beyond them.
	Tickets []Ticket
	More    int
}

// Event is one pulled event. Key is its identity in the feed and stays the same when it moves.
// Series is set on an occurrence of a recurring event; skipping a series matches it.
type Event struct {
	Key      string
	Series   string
	Title    string
	StartsAt time.Time
	EndsAt   *time.Time
}

// Ticket is one pulled ticket. Key is its identity, kept as the ref of the task it becomes.
type Ticket struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Notes string `json:"notes"`
	URL   string `json:"url"`
	// Rereview means the source is asking for this again, so a done or dropped task with its key
	// doesn't hide it, such as a pull request whose review was requested again.
	Rereview bool `json:"rereview,omitempty"`
}
