// Package tui is whatnow's terminal companion: today's list, the day strip, what now, and chat,
// over the same parts the server uses.
package tui

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jameynakama/whatnow/internal/app"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/stream"
)

type mode int

const (
	modeList mode = iota
	modeAdd
	modePass
	modeHelp
)

const (
	// wideEnough is the narrowest terminal that fits chat beside the list.
	wideEnough   = 90
	refreshEvery = 5 * time.Second
	toastFor     = 4 * time.Second
)

type toast struct {
	text string
	err  bool
	id   int
}

type (
	wroteMsg struct {
		batch int64
		toast string
	}
	undoneMsg       struct{}
	failedMsg       struct{ err error }
	toastExpiredMsg struct{ id int }
	pickedMsg       struct{ pick planner.Pick }
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
	toast         toast
	toastSeq      int
	lastBatch     int64 // the user's last change from this TUI, for u
	asking        bool  // what now is thinking
	sending       bool  // a chat reply is being written
	pick          *planner.Pick
	passing       int64 // the pick being passed on while its reason is typed
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
	case wroteMsg:
		m.lastBatch = msg.batch
		next, cmd := m.show(msg.toast+" · u to undo", false)
		return next, tea.Batch(cmd, next.load())
	case undoneMsg:
		m.lastBatch = 0
		next, cmd := m.show("Undone", false)
		return next, tea.Batch(cmd, next.load())
	case failedMsg:
		m.asking, m.sending = false, false
		return m.show(friendly(msg.err), true)
	case toastExpiredMsg:
		if msg.id == m.toast.id {
			m.toast = toast{}
		}
	case pickedMsg:
		m.asking = false
		p := msg.pick
		m.pick, m.cursor = &p, p.TaskID
		return m, m.load()
	case spinner.TickMsg:
		if !m.asking && !m.sending {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
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
	if m.pick != nil && !slices.Contains(s.order(), m.pick.TaskID) {
		m.pick = nil
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
	switch m.mode {
	case modeHelp:
		m.mode = modeList
		return m, nil
	case modeAdd, modePass:
		return m.onInput(k)
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
	case "w":
		if m.asking {
			return m, nil
		}
		m.asking = true
		return m, tea.Batch(m.whatNow(0, ""), m.spin.Tick)
	case "n":
		if m.pick == nil || m.asking {
			return m, nil
		}
		m.passing = m.pick.TaskID
		return m.prompt(modePass, "Why not? (optional)")
	case "a":
		return m.prompt(modeAdd, "What's on your mind?")
	case "u":
		if m.lastBatch == 0 {
			return m.show("Nothing to undo", false)
		}
		return m, m.undo(m.lastBatch)
	case " ", "p", "x":
		if t, ok := m.current(); ok {
			return m, m.act(k.String(), t)
		}
	}
	return m, nil
}

// onInput handles keys while the one-line input is open.
func (m Model) onInput(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		md := m.mode
		m.input.Reset()
		m.mode = modeList
		m.input.Blur()
		if md == modePass {
			m.asking = true
			return m, tea.Batch(m.whatNow(m.passing, text), m.spin.Tick)
		}
		if text == "" {
			return m, nil
		}
		return m, m.capture(text)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return m, cmd
}

func (m Model) prompt(md mode, placeholder string) (tea.Model, tea.Cmd) {
	m.mode = md
	m.input.Reset()
	m.input.Placeholder = placeholder
	return m, m.input.Focus()
}

// current is the task under the cursor, if the list has one.
func (m Model) current() (domain.Task, bool) {
	t, ok := m.snap.tasks[m.cursor]
	return t, ok
}

func (m Model) show(text string, isErr bool) (Model, tea.Cmd) {
	m.toastSeq++
	m.toast = toast{text: text, err: isErr, id: m.toastSeq}
	id := m.toastSeq
	return m, tea.Tick(toastFor, func(time.Time) tea.Msg { return toastExpiredMsg{id} })
}

func (m *Model) move(by int) {
	ids := m.snap.order()
	if len(ids) == 0 {
		return
	}
	at := slices.Index(ids, m.cursor) + by
	m.cursor = ids[min(max(at, 0), len(ids)-1)]
}
