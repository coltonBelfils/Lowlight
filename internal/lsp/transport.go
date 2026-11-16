package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Conn implements framed JSON-RPC 2.0 transport (`Content-Length: N\r\n\r\n<payload>`)
// over an `io.Reader` and `io.Writer` (typically `os.Stdin` and `os.Stdout`).
type Conn struct {
	r      *bufio.Reader
	w      io.Writer
	logW   io.Writer
	sendMu sync.Mutex
}

// NewConn creates a new JSON-RPC LSP transport connection.
func NewConn(r io.Reader, w io.Writer, logW io.Writer) *Conn {
	return &Conn{
		r:    bufio.NewReaderSize(r, 64*1024),
		w:    w,
		logW: logW,
	}
}

func (c *Conn) logf(format string, args ...any) {
	if c.logW != nil {
		_, _ = fmt.Fprintf(c.logW, "[lowlight-lsp] "+format+"\n", args...)
	}
}

// ReadMessage reads the next Content-Length framed JSON-RPC message from the input stream.
func (c *Conn) ReadMessage() (*RequestMessage, error) {
	contentLen := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" {
				return nil, io.EOF
			}
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			if contentLen >= 0 {
				break
			}
			// Ignore leading blank lines if any
			continue
		}
		colon := strings.IndexByte(trimmed, ':')
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:colon])
		val := strings.TrimSpace(trimmed[colon+1:])
		if strings.EqualFold(key, "Content-Length") {
			n, convErr := strconv.Atoi(val)
			if convErr != nil || n < 0 {
				return nil, fmt.Errorf("invalid Content-Length header: %q", val)
			}
			contentLen = n
		}
	}

	payload := make([]byte, contentLen)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		return nil, err
	}
	c.logf("recv (%d bytes): %s", contentLen, string(payload))

	var msg RequestMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return nil, fmt.Errorf("malformed JSON-RPC payload: %w", err)
	}
	return &msg, nil
}

// SendResponse writes a JSON-RPC 2.0 response for a request `id`.
func (c *Conn) SendResponse(id json.RawMessage, result any, respErr *ResponseError) error {
	var body []byte
	var err error
	if respErr != nil {
		body, err = json.Marshal(ErrorResponseMessage{
			JSONRPC: "2.0",
			ID:      id,
			Error:   respErr,
		})
	} else {
		body, err = json.Marshal(ResponseMessage{
			JSONRPC: "2.0",
			ID:      id,
			Result:  result,
		})
	}
	if err != nil {
		return err
	}
	return c.writeFrame(body)
}

// SendNotification writes a server-to-client JSON-RPC 2.0 notification (e.g. `textDocument/publishDiagnostics`).
func (c *Conn) SendNotification(method string, params any) error {
	body, err := json.Marshal(NotificationMessage{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}
	return c.writeFrame(body)
}

// SendRequest writes a server-to-client JSON-RPC 2.0 request (e.g. `window/showDocument`).
func (c *Conn) SendRequest(id int64, method string, params any) error {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	rawID := json.RawMessage([]byte(strconv.FormatInt(id, 10)))
	body, err := json.Marshal(RequestMessage{
		JSONRPC: "2.0",
		ID:      &rawID,
		Method:  method,
		Params:  rawParams,
	})
	if err != nil {
		return err
	}
	return c.writeFrame(body)
}

func (c *Conn) writeFrame(body []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	c.logf("send (%d bytes): %s", len(body), string(body))
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(c.w, header); err != nil {
		return err
	}
	_, err := c.w.Write(body)
	return err
}
