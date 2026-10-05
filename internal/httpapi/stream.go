package httpapi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// stream sends every hub message to the client as a server-sent event until the client leaves.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	msgs, unsubscribe := s.Hub.Subscribe()
	defer unsubscribe()

	rc := http.NewResponseController(w)
	fmt.Fprint(w, ": connected\n\n")
	if err := rc.Flush(); err != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-msgs:
			data, err := json.Marshal(m.Data)
			if err != nil {
				log.Printf("stream %s: %v", m.Type, err)
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.Type, data)
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
