package fake_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/llm/fake"
)

func TestCleanupSplitsANoteIntoTitles(t *testing.T) {
	tests := []struct {
		raw  string
		want []string
	}{
		{"buy milk", []string{"buy milk"}},
		{"buy milk and call mom", []string{"buy milk", "call mom"}},
		{"buy milk, call mom\nwater the plants", []string{"buy milk", "call mom", "water the plants"}},
		{"sand the candy dish", []string{"sand the candy dish"}},
		{" buy milk ,, and ", []string{"buy milk"}},
		{"and", []string{"and"}},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			reply, err := fake.Client{}.Complete(context.Background(), llm.Request{SchemaName: "cleanup", User: tt.raw})
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Original struct {
					Keep  bool   `json:"keep"`
					Title string `json:"title"`
				} `json:"original"`
				NewTasks []struct {
					Ref   string `json:"ref"`
					Title string `json:"title"`
				} `json:"new_tasks"`
			}
			if err := json.Unmarshal([]byte(reply), &got); err != nil {
				t.Fatal(err)
			}
			if !got.Original.Keep {
				t.Error("original not kept")
			}
			titles := []string{got.Original.Title}
			for i, n := range got.NewTasks {
				if want := "n" + string(rune('1'+i)); n.Ref != want {
					t.Errorf("ref %d = %q, want %q", i, n.Ref, want)
				}
				titles = append(titles, n.Title)
			}
			if diff := cmp.Diff(tt.want, titles); diff != "" {
				t.Errorf("titles (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAPlainRequestGetsTheFixedSentence(t *testing.T) {
	reply, err := fake.Client{}.Complete(context.Background(), llm.Request{User: "Reply with the single word: ok"})
	if err != nil {
		t.Fatal(err)
	}
	if reply != fake.Sentence {
		t.Errorf("reply = %q, want %q", reply, fake.Sentence)
	}
}

func TestChatStreamsTheCannedReplyWithoutTools(t *testing.T) {
	var streamed []string
	reply, err := fake.Client{}.Chat(context.Background(), llm.ChatRequest{}, func(s string) { streamed = append(streamed, s) })
	if err != nil {
		t.Fatal(err)
	}
	if len(streamed) < 2 {
		t.Errorf("streamed in %d pieces, want several", len(streamed))
	}
	if got := strings.Join(streamed, ""); got != fake.ChatReply {
		t.Errorf("streamed %q, want %q", got, fake.ChatReply)
	}
	if reply.Text != fake.ChatReply || len(reply.ToolCalls) != 0 {
		t.Errorf("reply = %+v", reply)
	}
}
