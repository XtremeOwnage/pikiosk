package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// cdp is a minimal Chrome DevTools Protocol client bound to one page target.
type cdp struct {
	ws      *websocket.Conn
	writeMu sync.Mutex
	nextID  atomic.Int64

	onEvent func(method string, params json.RawMessage)

	mu      sync.Mutex
	pending map[int64]chan cdpResponse
	closed  chan struct{}
	err     error
}

type cdpResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type cdpMessage struct {
	ID int64 `json:"id"`
	cdpResponse
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type target struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func findPage(ctx context.Context, port int) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json/list", port), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var targets []target
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return "", err
	}
	for _, t := range targets {
		if t.Type == "page" && t.WebSocketDebuggerURL != "" {
			return t.WebSocketDebuggerURL, nil
		}
	}
	return "", errors.New("no page target")
}

func dial(ctx context.Context, wsURL string, onEvent func(string, json.RawMessage)) (*cdp, error) {
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	ws, _, err := d.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(64 << 20)
	c := &cdp{ws: ws, onEvent: onEvent, pending: map[int64]chan cdpResponse{}, closed: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

func (c *cdp) readLoop() {
	var err error
	for {
		var msg cdpMessage
		if err = c.ws.ReadJSON(&msg); err != nil {
			break
		}
		if msg.ID == 0 {
			if c.onEvent != nil && msg.Method != "" {
				c.onEvent(msg.Method, msg.Params)
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[msg.ID]
		delete(c.pending, msg.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- msg.cdpResponse
		}
	}
	c.mu.Lock()
	c.err = err
	c.pending = nil
	c.mu.Unlock()
	close(c.closed)
}

func (c *cdp) Done() <-chan struct{} { return c.closed }

func (c *cdp) Close() { _ = c.ws.Close() }

// Call sends a command and decodes its result into out (which may be nil).
func (c *cdp) Call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan cdpResponse, 1)
	c.mu.Lock()
	if c.pending == nil {
		c.mu.Unlock()
		return errors.New("devtools connection closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if params == nil {
		params = struct{}{}
	}
	c.writeMu.Lock()
	err := c.ws.WriteJSON(map[string]any{"id": id, "method": method, "params": params})
	c.writeMu.Unlock()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return fmt.Errorf("%s: %s", method, resp.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	case <-c.closed:
		return errors.New("devtools connection closed")
	case <-ctx.Done():
		c.mu.Lock()
		if c.pending != nil {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}
