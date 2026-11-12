// Package wire sends single provider requests. It never retries and never
// puts the key into errors.
package wire

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// Reply is a finished HTTP exchange. Status 0 means no response arrived.
type Reply struct {
	Status int
	Header http.Header
	Body   []byte
}

var (
	client = &http.Client{Timeout: 30 * time.Second}
	// slow allows for uploading and recognizing up to 100 MB of audio.
	slow = &http.Client{Timeout: 10 * time.Minute}
)

// Do sends one request. err is only set when no HTTP response arrived.
func Do(ctx context.Context, method, url string, header http.Header, body []byte) (Reply, error) {
	return send(ctx, client, method, url, header, body)
}

// DoSlow is Do for requests that carry or produce a lot of audio.
func DoSlow(ctx context.Context, method, url string, header http.Header, body []byte) (Reply, error) {
	return send(ctx, slow, method, url, header, body)
}

func send(ctx context.Context, client *http.Client, method, url string, header http.Header, body []byte) (Reply, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return Reply{}, err
	}
	req.Header = header
	resp, err := client.Do(req)
	if err != nil {
		return Reply{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return Reply{Status: resp.StatusCode, Header: resp.Header, Body: data}, err
}

// Dial opens a WebSocket. On a refused upgrade the returned Reply holds the
// HTTP status, headers and body, and conn is nil.
func Dial(ctx context.Context, url string, header http.Header) (*websocket.Conn, Reply, error) {
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err == nil {
		return conn, Reply{Status: resp.StatusCode, Header: resp.Header}, nil
	}
	if resp == nil {
		return nil, Reply{}, err
	}
	reply := Reply{Status: resp.StatusCode, Header: resp.Header}
	if resp.Body != nil {
		reply.Body, _ = io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
	}
	return nil, reply, err
}
