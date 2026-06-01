package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type Response struct {
	Result json.RawMessage
	Error  *RPCError
}

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("RPC Error %d: %s", e.Code, e.Message)
}

type Client struct {
	conn net.Conn

	mu      sync.Mutex // guards nextID + pending map only
	nextID  int64
	pending map[int64]chan Response

	writeMu sync.Mutex // serializes socket writes, held only for the write

	doneMu sync.Mutex
	done   chan struct{}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
	ID      int64  `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

func Connect(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}

	c := &Client{
		conn:    conn,
		pending: make(map[int64]chan Response),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	// Allocate an ID and register the pending channel under the lightweight mu.
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	respCh := make(chan Response, 1)
	c.pending[id] = respCh
	c.mu.Unlock()

	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      id,
	}
	data, err := json.Marshal(req)
	if err != nil {
		c.removePending(id)
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	data = append(data, '\n')

	// Serialize the write under a dedicated lock (NOT c.mu), so a large prompt
	// write doesn't block response dispatch for all other in-flight calls.
	c.writeMu.Lock()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(dl)
	} else {
		_ = c.conn.SetWriteDeadline(time.Now().Add(60 * time.Second))
	}
	_, werr := c.conn.Write(data)
	_ = c.conn.SetWriteDeadline(time.Time{})
	c.writeMu.Unlock()

	if werr != nil {
		c.removePending(id)
		return nil, fmt.Errorf("failed to write request: %w", werr)
	}

	select {
	case <-ctx.Done():
		c.removePending(id)
		return nil, ctx.Err()
	case resp := <-respCh:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-c.doneChan():
		return nil, fmt.Errorf("connection closed")
	}
}

func (c *Client) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) doneChan() chan struct{} {
	c.doneMu.Lock()
	defer c.doneMu.Unlock()
	return c.done
}

func (c *Client) Close() error {
	c.doneMu.Lock()
	select {
	case <-c.done:
		// already closed
	default:
		close(c.done)
	}
	c.doneMu.Unlock()
	return c.conn.Close()
}

func (c *Client) Reconnect(socketPath string) error {
	_ = c.Close()

	time.Sleep(500 * time.Millisecond) // Give daemon time to start

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to reconnect: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.pending = make(map[int64]chan Response)
	c.mu.Unlock()

	c.doneMu.Lock()
	c.done = make(chan struct{})
	c.doneMu.Unlock()

	go c.readLoop()
	return nil
}

func (c *Client) readLoop() {
	scanner := bufio.NewScanner(c.conn)
	// DeepSeek responses can be large (up to 384k tokens), bump max scan token size
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024*10)

	for scanner.Scan() {
		line := scanner.Bytes()
		var res rpcResponse
		if err := json.Unmarshal(line, &res); err != nil {
			// G2: don't silently drop — a malformed frame would otherwise leave
			// the matching caller blocked until its context expires.
			log.Printf("daemon client: failed to decode response frame: %v", err)
			continue
		}

		c.mu.Lock()
		ch, ok := c.pending[res.ID]
		if ok {
			delete(c.pending, res.ID)
		}
		c.mu.Unlock()

		if ok {
			ch <- Response{Result: res.Result, Error: res.Error}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("daemon client: read loop ended: %v", err)
	}

	c.mu.Lock()
	for id, ch := range c.pending {
		ch <- Response{Error: &RPCError{Code: -32000, Message: "Connection closed"}}
		delete(c.pending, id)
	}
	c.mu.Unlock()
}
