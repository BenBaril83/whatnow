package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/agent"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm/fake"
	"github.com/jameynakama/whatnow/internal/stream"
)

func TestChatStreamsAReply(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	tm := f.run(t)
	waitFor(t, tm, "buy milk")
	tm.Send(key("c"))
	tm.Type("hello")
	tm.Send(key("enter"))
	waitFor(t, tm, fake.ChatReply)
	quit(t, tm)

	history, err := f.app.Agent.History(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Text != "hello" || history[1].Text != fake.ChatReply {
		t.Errorf("history = %+v", history)
	}
}

func TestStreamedPiecesEndInOneMessage(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := send(f.model(t, 120, 30),
		hubMsg(stream.Message{Type: "chat.delta", Data: agent.Delta{Text: "A fake "}}),
		hubMsg(stream.Message{Type: "chat.delta", Data: agent.Delta{Text: "reply."}}))
	if m.streaming != "A fake reply." {
		t.Fatalf("streaming = %q", m.streaming)
	}
	if !strings.Contains(m.View(), "A fake reply.") {
		t.Errorf("the reply isn't shown while it streams:\n%s", m.View())
	}
	m = send(m, hubMsg(stream.Message{Type: "chat.message", Data: domain.Message{Role: domain.RoleAssistant, Text: "A fake reply."}}))
	if m.streaming != "" || len(m.snap.chat) != 1 {
		t.Errorf("streaming = %q, chat = %+v", m.streaming, m.snap.chat)
	}
}

func TestChatOpensBesideTheListOnAWideTerminal(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	if v := f.model(t, 120, 30).View(); !strings.Contains(v, "c to chat") || !strings.Contains(v, "buy milk") {
		t.Errorf("view:\n%s", v)
	}
}

func TestChatReplacesTheListOnANarrowTerminalUntilEsc(t *testing.T) {
	f := newFixture(t)
	f.task(t, "buy milk")
	f.start(t)
	m := f.model(t, 80, 24)
	if m.chatOpen {
		t.Fatal("chat starts open on a narrow terminal")
	}
	m = send(m, key("c"))
	if v := m.View(); strings.Contains(v, "buy milk") {
		t.Errorf("the list shows beside chat at 80 columns:\n%s", v)
	}
	m = send(m, key("esc"))
	if m.chatOpen || m.mode != modeList {
		t.Errorf("esc left chat open (%v) or mode %d", m.chatOpen, m.mode)
	}
}

func TestEnterWhileAReplyIsBeingWrittenWaits(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := send(f.model(t, 120, 30), key("c"))
	m.sending = true
	m = send(m, key("h"), key("i"))
	if _, cmd := m.Update(key("enter")); cmd != nil {
		t.Error("a second message was sent while the first reply was being written")
	}
}

func TestATooLongChatMessageShowsTheLimit(t *testing.T) {
	f := newFixture(t)
	f.today(t)
	m := f.model(t, 120, 30)
	if v := send(m, m.send(strings.Repeat("x", 5000))()).View(); !strings.Contains(v, "limit") {
		t.Errorf("view:\n%s", v)
	}
}
