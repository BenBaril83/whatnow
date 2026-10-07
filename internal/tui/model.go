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
	"github.com/charmbracelet/lipgloss"

	"github.com/jameynakama/whatnow/internal/agent"
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
	modeChat
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
	chatSentMsg     struct{}
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
	passing       int64                   // the pick being passed on while its reason is typed
	streaming     string                  // the chat reply so far, while it's written
	working       map[stream.WorkKind]int // the server's slow model calls in flight, by kind
	cheer         cheer
	cheerFrame    int
	cheerSeq      int
	morning       morning
}

// New makes the TUI and subscribes it to the hub. webURL is where the user wraps up the day.
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
		working:     map[stream.WorkKind]int{},
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
		// Below wideEnough chat takes the list's place, so it closes unless the user is in it.
		if msg.Width < wideEnough && m.mode != modeChat {
			m.chatOpen = false
		}
		m.width, m.height = msg.Width, msg.Height
		m.fitInput()
	case snapshotMsg:
		m.replace(msg.snap)
		// A day started in the browser ends the ritual here, short of this TUI's own start.
		if msg.snap.today.Plan != nil && m.morning.step != stepNone && m.morning.step < stepMaking {
			m.morning = morning{}
			m.input.Blur()
		}
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
	case chatSentMsg:
		m.sending = false
		return m, m.load()
	case reviewedMsg, reviewFailedMsg, startedMsg, startFailedMsg, conflictMsg:
		return m.onMorningMsg(msg)
	case cheerTickMsg:
		return m.onCheerTick(msg)
	case spinner.TickMsg:
		if !m.spinning() {
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
	switch msg.Type {
	case "chat.delta":
		m.streaming += msg.Data.(agent.Delta).Text
		return m, m.listen()
	case "chat.message":
		said := msg.Data.(domain.Message)
		if said.Role == domain.RoleAssistant {
			m.streaming = ""
		}
		if m.snap != nil {
			m.snap.chat = append(m.snap.chat, said)
		}
		return m, m.listen()
	case "work.started", "work.finished":
		was := m.spinning()
		w := msg.Data.(stream.Work)
		if msg.Type == "work.started" {
			m.working[w.Kind]++
		} else {
			m.working[w.Kind] = max(m.working[w.Kind]-1, 0)
		}
		if !was && m.spinning() {
			return m, tea.Batch(m.listen(), m.spin.Tick)
		}
		return m, m.listen()
	case "batch.applied":
		cmd := m.startCheer(cheerFor(msg.Data.(stream.BatchApplied), m.snap))
		return m, tea.Batch(m.listen(), m.load(), cmd)
	}
	return m, tea.Batch(m.listen(), m.load())
}

// spinning says something is in flight: what now, a chat reply, or the server's own model work.
func (m Model) spinning() bool {
	return m.asking || m.sending || m.morning.step == stepGreet || m.morning.step == stepMaking || m.working[stream.WorkReplan] > 0 || m.working[stream.WorkCleanup] > 0
}

func (m Model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeHelp:
		m.mode = modeList
		return m, nil
	case modeAdd, modePass, modeChat:
		return m.onInput(k)
	}
	if m.morning.step != stepNone {
		return m.onMorningKey(k)
	}
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "r":
		return m, m.load()
	}
	if m.snap == nil {
		return m, nil
	}
	if m.snap.today.Plan == nil {
		if k.String() == "enter" {
			return m.beginMorning()
		}
		return m, nil
	}
	// Once the day is wrapped there's no list to act on: adding, undo, chat, and help still work.
	if m.wrapped() {
		switch k.String() {
		case "a", "u", "c", "?":
		default:
			return m, nil
		}
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
	case "c":
		m.chatOpen = true
		return m.prompt(modeChat, "Message")
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
		if m.mode == modeChat && m.width < wideEnough {
			m.chatOpen = false
		}
		m.mode = modeList
		m.input.Blur()
		return m, nil
	case "enter":
		if m.mode == modeChat {
			text := strings.TrimSpace(m.input.Value())
			if text == "" || m.sending {
				return m, nil
			}
			m.input.Reset()
			m.sending = true
			return m, tea.Batch(m.send(text), m.spin.Tick)
		}
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
	m.fitInput()
	return m, m.input.Focus()
}

// fitInput sizes the inputs to where they're drawn, so long text scrolls instead of being cut off:
// the chat pane for chat, the morning card for the mood and the dump, the footer otherwise.
func (m *Model) fitInput() {
	w := m.width
	switch {
	case m.mode == modeChat:
		w = m.chatPaneWidth() - 5
	case m.morning.step == stepPace:
		w = min(cardWidth, m.width) - 5
	case m.morning.step == stepDump:
		m.morning.dump.SetWidth(max(min(cardWidth, m.width)-7, 1))
	}
	m.input.Width = max(w-lipgloss.Width(m.input.Prompt)-1, 1)
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
