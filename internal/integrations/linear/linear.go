// Package linear offers tickets from Linear, through a personal API key: the issues assigned to
// you that are active, or the issues of a view.
package linear

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jameynakama/whatnow/internal/integrations"
)

const defaultEndpoint = "https://api.linear.app/graphql"

// Linear has no total count, so one request asks twice: the issues to show, and more ids to count
// what the pick list leaves out. Past the second count, the number undercounts.
const (
	shownFields = `{nodes{identifier title description url assignee{id}}}`
	activeState = `filter: {state: {type: {in: ["unstarted", "started"]}}}`

	activeQuery = `query { viewer { ` +
		`shown: assignedIssues(first: 50, ` + activeState + `) ` + shownFields + ` ` +
		`rest: assignedIssues(first: 250, ` + activeState + `) {nodes{id}} } }`
	viewQuery = `query($id: String!) { customView(id: $id) { ` +
		`shown: issues(first: 50) ` + shownFields + ` ` +
		`rest: issues(first: 250) {nodes{id}} } }`
	// viewFilterQuery is viewQuery with the filter Linear's UI adds to a view's address.
	viewFilterQuery = `query($id: String!, $filter: IssueFilter) { customView(id: $id) { ` +
		`shown: issues(first: 50, filter: $filter) ` + shownFields + ` ` +
		`rest: issues(first: 250, filter: $filter) {nodes{id}} } }`
)

var (
	errUnreachable = errors.New("Can't reach Linear")
	errRejected    = errors.New("Linear rejected the API key")
	errUnreadable  = errors.New("couldn't read Linear's answer")
	errBadView     = errors.New("that isn't a Linear view's address")
)

// Linear is the Linear integration.
type Linear struct {
	client   *http.Client
	endpoint string
}

// New returns the integration. A nil client means one that gives up after 30 seconds.
func New(client *http.Client) *Linear {
	return NewAt(client, defaultEndpoint)
}

// NewAt is New for a Linear at another address, such as a fake in a test.
func NewAt(client *http.Client, endpoint string) *Linear {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Linear{client: client, endpoint: endpoint}
}

func (*Linear) ID() string                { return "linear" }
func (*Linear) Name() string              { return "Linear" }
func (*Linear) Pulls() integrations.Pulls { return integrations.PullsTickets }

func (*Linear) Fields() []integrations.Field {
	return []integrations.Field{
		{Key: "api_key", Label: "API key", Kind: integrations.Secret, Required: true, Help: "In Linear: Settings → Security & access → Personal API keys"},
		{Key: "view", Label: "View", Kind: integrations.Text, Help: "Optional: a view's address, such as https://linear.app/acme/view/my-bugs-0a1b2c3d4e5f. Blank pulls your active assigned issues"},
	}
}

// Pull runs the search. The window is ignored: a ticket isn't for a day.
func (l *Linear) Pull(ctx context.Context, cfg integrations.Config, _, _ time.Time) []integrations.Source {
	src := integrations.Source{Label: "Linear"}
	src.Tickets, src.More, src.Err = l.search(ctx, cfg)
	return []integrations.Source{src}
}

type issues struct {
	Nodes []struct {
		ID          string  `json:"id"`
		Identifier  string  `json:"identifier"`
		Title       string  `json:"title"`
		Description *string `json:"description"`
		URL         string  `json:"url"`
		Assignee    *struct {
			ID string `json:"id"`
		} `json:"assignee"`
	} `json:"nodes"`
}

type scope struct {
	Shown issues `json:"shown"`
	Rest  issues `json:"rest"`
}

type answer struct {
	Data *struct {
		Viewer     *scope `json:"viewer"`
		CustomView *scope `json:"customView"`
	} `json:"data"`
	Errors []struct {
		Message    string `json:"message"`
		Extensions struct {
			Code string `json:"code"`
		} `json:"extensions"`
	} `json:"errors"`
}

// viewID is the slug ID of a view's address: the part after the last "-" of its last path segment.
// A "filter" in the address's query is returned as the object it encodes, nil when there is none.
func viewID(addr string) (string, map[string]any, bool) {
	u, err := url.Parse(strings.TrimSpace(addr))
	if err != nil || u.Scheme != "https" || u.Host != "linear.app" {
		return "", nil, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "view" || parts[2] == "" {
		return "", nil, false
	}
	slug := parts[2]
	id := slug[strings.LastIndex(slug, "-")+1:]
	if id == "" {
		return "", nil, false
	}
	encoded := u.Query().Get("filter")
	if encoded == "" {
		return id, nil, true
	}
	filter, ok := decodeFilter(encoded)
	return id, filter, ok
}

// decodeFilter reads a JSON object from base64, standard or URL-safe, padded or not. A query string
// turns a standard "+" into a space, so that is undone first.
func decodeFilter(encoded string) (map[string]any, bool) {
	encoded = strings.NewReplacer(" ", "-", "+", "-", "/", "_").Replace(strings.TrimRight(encoded, "="))
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}
	var filter map[string]any
	if json.Unmarshal(raw, &filter) != nil || filter == nil {
		return nil, false
	}
	return filter, true
}

// search returns the matching tickets, assigned ones first, and how many more matched. Its errors
// never wrap the client's, and never hold the key.
func (l *Linear) search(ctx context.Context, cfg integrations.Config) ([]integrations.Ticket, int, error) {
	payload := struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables,omitempty"`
	}{Query: activeQuery}
	if addr := strings.TrimSpace(cfg.Values["view"]); addr != "" {
		id, filter, ok := viewID(addr)
		if !ok {
			return nil, 0, errBadView
		}
		payload.Query, payload.Variables = viewQuery, map[string]any{"id": id}
		if filter != nil {
			payload.Query, payload.Variables["filter"] = viewFilterQuery, filter
		}
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, errUnreadable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, 0, errUnreachable
	}
	req.Header.Set("Authorization", cfg.Values["api_key"])
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, 0, errUnreachable
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, errUnreachable
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, 0, errRejected
	}
	var res answer
	parsed := json.Unmarshal(body, &res) == nil
	// GraphQL reports its errors on a 400 or a 200 alike.
	if parsed && len(res.Errors) > 0 && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusBadRequest) {
		msgs := make([]string, 0, len(res.Errors))
		for _, e := range res.Errors {
			if e.Extensions.Code == "AUTHENTICATION_ERROR" {
				return nil, 0, errRejected
			}
			msgs = append(msgs, e.Message)
		}
		return nil, 0, errors.New(strings.Join(msgs, " "))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("Linear answered %d", resp.StatusCode)
	}
	var found *scope
	if parsed && res.Data != nil {
		found = res.Data.Viewer
		if payload.Variables != nil {
			found = res.Data.CustomView
		}
	}
	if found == nil {
		return nil, 0, errUnreadable
	}
	tickets := make([]integrations.Ticket, 0, len(found.Shown.Nodes))
	assigned := map[string]bool{}
	for _, is := range found.Shown.Nodes {
		t := integrations.Ticket{Key: is.Identifier, Title: is.Identifier + " " + is.Title, URL: is.URL}
		if is.Description != nil {
			t.Notes = *is.Description
		}
		assigned[is.Identifier] = is.Assignee != nil
		tickets = append(tickets, t)
	}
	sort.SliceStable(tickets, func(a, b int) bool { return assigned[tickets[a].Key] && !assigned[tickets[b].Key] })
	return tickets, max(len(found.Rest.Nodes)-len(found.Shown.Nodes), 0), nil
}
