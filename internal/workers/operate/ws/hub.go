package ws

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"camunda-workers/internal/models"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		// TODO: lock this down to your frontend origin in production
		return true
	},
}

type client struct {
	conn   *websocket.Conn
	send   chan []byte
	closed bool
}

// Hub manages WebSocket connections and broadcasts Operate events.
type Hub struct {
	mu        sync.RWMutex
	clients   map[*client]struct{}
	broadcast chan models.WSEvent
}

func NewHub() *Hub {
	h := &Hub{
		clients:   make(map[*client]struct{}),
		broadcast: make(chan models.WSEvent, 512),
	}
	go h.run()
	return h
}

// Publish sends an event to all connected frontend clients.
// Safe to call from any goroutine (ES poller, Zeebe listener, etc).
func (h *Hub) Publish(eventType models.WSEventType, payload interface{}) {
	h.broadcast <- models.WSEvent{
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

// ServeWS is the Gin handler — mount at GET /operate/ws
func (h *Hub) ServeWS(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	cl := &client{
		conn: conn,
		send: make(chan []byte, 64),
	}

	h.mu.Lock()
	h.clients[cl] = struct{}{}
	h.mu.Unlock()

	// Write pump
	go func() {
		defer func() {
			conn.Close()
			h.mu.Lock()
			delete(h.clients, cl)
			h.mu.Unlock()
		}()
		for msg := range cl.send {
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()

	// Read pump — detects disconnect, sends pings
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		default:
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}
}

func (h *Hub) run() {
	for event := range h.broadcast {
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		h.mu.RLock()
		for cl := range h.clients {
			select {
			case cl.send <- data:
			default:
				// slow client — drop message rather than block broadcast
			}
		}
		h.mu.RUnlock()
	}
}
