// Package tui is whatnow's terminal companion: today's list, the day strip, what now, and chat,
// over the same parts the server uses.
package tui

import (
	"context"
	"slices"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jameynakama/whatnow/internal/app"
	"github.com/jameynakama/whatnow/internal/stream"
)

type mode int

const (
	modeList mode = iota
	modeHelp
)

const (
	// wideEnough is the narrowest terminal that fits chat beside the list.
	wideEnough   = 90
	refreshEvery = 5 * time.Second
	toastFor     = 4 * time.Second
)

type (
	snapshotMsg   struct{ snap snapshot }
	loadFailedMsg struct{ err error }
	tickMsg       struct{}
	hubMsg        stream.Message
)

// Model is the TUI. Its snapshot is replaced on every reload; everything else is the user's place
// on the screen.
type Model struct {
	app         *app.App
	ctx         context.Context
	webURL      string
	hub         <-chan stream.Message
	unsubscribe func()

	snap          *snapshot
	stale         bool
	loadErr       error
	width, height int
	cursor        int64 // the task under the cursor; 0 when the list is empty
	mode          mode
	input         textinput.Model
	spin          spinner.Model
	chatOpen      bool
}

// New makes the TUI and subscribes it to the hub. webURL is where the user starts and wraps the day.
func New(a *app.App, webURL string) Model {
	hub, unsubscribe := a.Hub.Subscribe()
	in := textinput.New()
	in.Prompt = "› "
	return Model{
		app:         a,
		ctx:         context.Background(),
		webURL:      webURL,
		hub:         hub,
		unsubscribe: unsubscribe,
		input:       in,
		spin:        spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
}

// Run shows the TUI on the terminal until the user quits.
func Run(a *app.App, webURL string) error {
	m := New(a, webURL)
	defer m.unsubscribe()
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.load(), m.listen(), tick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if m.width == 0 {
			m.chatOpen = msg.Width >= wideEnough
		}
		m.width, m.height = msg.Width, msg.Height
	case snapshotMsg:
		m.replace(msg.snap)
	case loadFailedMsg:
		m.stale, m.loadErr = true, msg.err
	case tickMsg:
		return m, tea.Batch(m.load(), tick())
	case hubMsg:
		return m.onHub(stream.Message(msg))
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

// replace takes a new snapshot. The cursor stays on its task; if that task left the list, it
// takes the task now at the same place.
func (m *Model) replace(s snapshot) {
	at := 0
	if m.snap != nil {
		at = max(slices.Index(m.snap.order(), m.cursor), 0)
	}
	m.snap, m.stale, m.loadErr = &s, false, nil
	ids := s.order()
	if slices.Contains(ids, m.cursor) {
		return
	}
	m.cursor = 0
	if len(ids) > 0 {
		m.cursor = ids[min(at, len(ids)-1)]
	}
}

func (m Model) onHub(msg stream.Message) (tea.Model, tea.Cmd) {
	return m, tea.Batch(m.listen(), m.load())
}

func (m Model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.mode == modeHelp {
		m.mode = modeList
		return m, nil
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "r":
		return m, m.load()
	}
	if m.snap == nil || m.snap.today.Plan == nil {
		return m, nil
	}
	switch k.String() {
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "?":
		m.mode = modeHelp
	}
	return m, nil
}

func (m *Model) move(by int) {
	ids := m.snap.order()
	if len(ids) == 0 {
		return
	}
	at := slices.Index(ids, m.cursor) + by
	m.cursor = ids[min(max(at, 0), len(ids)-1)]
}
