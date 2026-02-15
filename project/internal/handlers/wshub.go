package handlers

import (
	"encoding/json"
	"sync"

	"github.com/gorilla/websocket"
)

// wsClient wraps a WebSocket connection with a write mutex.
// gorilla/websocket does not allow concurrent WriteMessage calls,
// so every write must be serialized per connection.
type wsClient struct {
	conn *websocket.Conn
	wmu  sync.Mutex
}

// WSHub manages WebSocket clients grouped by topic.
// When data changes, call Broadcast(topic, event) to push to all subscribers.
type WSHub struct {
	mu      sync.RWMutex
	clients map[string]map[*wsClient]struct{} // topic -> set of clients
	lookup  map[*websocket.Conn]*wsClient     // conn -> client (for Unsubscribe)
}

// WSEvent is the payload sent to WebSocket clients.
type WSEvent struct {
	Type string      `json:"type"` // e.g. "battery_update", "flight_phase"
	Data interface{} `json:"data,omitempty"`
}

var hub = &WSHub{
	clients: make(map[string]map[*wsClient]struct{}),
	lookup:  make(map[*websocket.Conn]*wsClient),
}

// Subscribe adds a WebSocket connection to a topic.
func (h *WSHub) Subscribe(topic string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cl := &wsClient{conn: conn}
	if h.clients[topic] == nil {
		h.clients[topic] = make(map[*wsClient]struct{})
	}
	h.clients[topic][cl] = struct{}{}
	h.lookup[conn] = cl
}

// Unsubscribe removes a WebSocket connection from a topic.
func (h *WSHub) Unsubscribe(topic string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cl, ok := h.lookup[conn]
	if !ok {
		return
	}
	delete(h.lookup, conn)
	if conns, exists := h.clients[topic]; exists {
		delete(conns, cl)
		if len(conns) == 0 {
			delete(h.clients, topic)
		}
	}
}

// Broadcast sends an event to all clients subscribed to the given topic.
// Failed writes cause the connection to be removed silently.
func (h *WSHub) Broadcast(topic string, event WSEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	h.mu.RLock()
	snapshot := make([]*wsClient, 0, len(h.clients[topic]))
	for cl := range h.clients[topic] {
		snapshot = append(snapshot, cl)
	}
	h.mu.RUnlock()

	var failed []*wsClient
	for _, cl := range snapshot {
		cl.wmu.Lock()
		err := cl.conn.WriteMessage(websocket.TextMessage, data)
		cl.wmu.Unlock()
		if err != nil {
			failed = append(failed, cl)
		}
	}

	// clean up dead connections
	if len(failed) > 0 {
		h.mu.Lock()
		for _, cl := range failed {
			if conns, ok := h.clients[topic]; ok {
				delete(conns, cl)
			}
			delete(h.lookup, cl.conn)
			cl.conn.Close()
		}
		h.mu.Unlock()
	}
}
