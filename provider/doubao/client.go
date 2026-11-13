package doubao

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/yangwenmai/voxbridge/core"
	"github.com/yangwenmai/voxbridge/media"
	"github.com/yangwenmai/voxbridge/provider/internal/wire"
)

// RetryDelays are the waits before each retry of a retryable failure.
var RetryDelays = []time.Duration{1 * time.Second, 3 * time.Second, 8 * time.Second}

// Client calls the Doubao HTTP services with retries and keeps usage.
type Client struct {
	key  string
	send func(ctx context.Context, method, url string, h http.Header, body []byte) (wire.Reply, error)
	wait func(ctx context.Context, d time.Duration) error
	// probe and encode read the local audio for recognition.
	probe  func(ctx context.Context, url string, headers map[string]string) (media.Probe, error)
	encode func(ctx context.Context, in core.AudioInput, offsetMs, durationMs int64) ([]byte, error)

	mu       sync.Mutex
	requests map[core.Capability]int
	units    map[core.Capability]float64
	cost     float64
}

func NewClient(key string) *Client {
	return &Client{
		key:      key,
		send:     wire.DoSlow,
		wait:     sleep,
		probe:    media.FFprobe,
		encode:   encodeChunk,
		requests: map[core.Capability]int{},
		units:    map[core.Capability]float64{},
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (Provider) Dubbing(key string) core.Dubbing {
	c := NewClient(key)
	return core.Dubbing{Recognizer: c, Translator: c, Synthesizer: c, Usage: c.Usage}
}

func (c *Client) Usage() core.Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	u := core.Usage{
		Requests: map[core.Capability]int{},
		Units:    map[core.Capability]float64{},
		UnitNames: map[core.Capability]string{
			core.CapRecognize: "秒音频", core.CapTranslate: "token", core.CapSynthesize: "字符",
		},
		Cost:     c.cost,
		Currency: "CNY",
	}
	for k, v := range c.requests {
		u.Requests[k] = v
	}
	for k, v := range c.units {
		u.Units[k] = v
	}
	return u
}

func (c *Client) bill(cap core.Capability, units, cost float64) {
	c.mu.Lock()
	c.units[cap] += units
	c.cost += cost
	c.mu.Unlock()
}

// call is one logical request: it POSTs body and lets check judge the
// reply, retrying retryable failures after RetryDelays.
type call struct {
	cap      core.Capability
	service  string
	url      string
	resource string
	header   http.Header
	body     any
	// check returns nil when reply is a success.
	check func(wire.Reply) *core.ProviderError
}

func (c *Client) do(ctx context.Context, k call) (wire.Reply, error) {
	payload, err := json.Marshal(k.body)
	if err != nil {
		return wire.Reply{}, err
	}
	for attempt := 0; ; attempt++ {
		h := headers(c.key, k.resource)
		for name, v := range k.header {
			h[name] = v
		}
		c.mu.Lock()
		c.requests[k.cap]++
		c.mu.Unlock()
		reply, err := c.send(ctx, http.MethodPost, k.url, h, payload)
		var failure error
		retry := false
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return wire.Reply{}, ctx.Err()
			}
			failure = errors.New(core.Scrub(err.Error(), c.key))
			retry = transient(err)
		default:
			if pe := k.check(reply); pe != nil {
				pe.Message = core.Scrub(pe.Message, c.key)
				if pe.LogID == "" {
					pe.LogID = reply.Header.Get("X-Tt-Logid")
				}
				failure, retry = pe, pe.Kind.Retryable()
			} else {
				return reply, nil
			}
		}
		if !retry || attempt >= len(RetryDelays) {
			return wire.Reply{}, failure
		}
		if err := c.wait(ctx, RetryDelays[attempt]); err != nil {
			return wire.Reply{}, err
		}
	}
}

func transient(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op) || errors.Is(err, context.DeadlineExceeded)
}

// checkJSON judges a reply whose body carries code and message.
func checkJSON(service string) func(wire.Reply) *core.ProviderError {
	return func(r wire.Reply) *core.ProviderError {
		code, msg := parseReply(r.Body)
		return Classify(service, r.Status, code, msg, r.Header.Get("X-Tt-Logid"))
	}
}
