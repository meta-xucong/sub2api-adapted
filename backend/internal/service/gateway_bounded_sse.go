package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Cancellation still permits final billing usage to arrive, but never extends
// indefinitely when an upstream keeps sending pings or leaves its body open.
const upstreamCancelDrainTimeout = 5 * time.Second

var errUpstreamCancelDrainTimeout = errors.New("upstream cancellation drain timeout")

// boundedSSEScanner keeps all parsing and downstream writes on the caller's
// goroutine. Watchdogs only close the HTTP body, whose Close unblocks Read.
// There is no scanner goroutine left waiting to deliver data after return.
type boundedSSEScanner struct {
	scanner               *bufio.Scanner
	body                  io.ReadCloser
	idle, drain           time.Duration
	mu                    sync.Mutex
	idleTimer, drainTimer *time.Timer
	idleGeneration        uint64
	stopped               bool
	cause                 error
	stopContext           func() bool
	closeOnce             sync.Once
}

func newBoundedSSEScanner(ctx context.Context, body io.ReadCloser, scanner *bufio.Scanner, idle, drain time.Duration) *boundedSSEScanner {
	p := &boundedSSEScanner{scanner: scanner, body: body, idle: idle, drain: drain}
	if ctx != nil {
		p.stopContext = context.AfterFunc(ctx, p.startDrain)
	}
	return p
}

func (p *boundedSSEScanner) expire(generation uint64, idle bool, cause error) {
	p.mu.Lock()
	if p.stopped || p.cause != nil || (idle && generation != p.idleGeneration) {
		p.mu.Unlock()
		return
	}
	p.cause = cause
	p.mu.Unlock()
	p.closeBody()
}

func (p *boundedSSEScanner) startDrain() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.drainTimer != nil {
		return
	}
	p.drainTimer = time.AfterFunc(p.drain, func() { p.expire(0, false, errUpstreamCancelDrainTimeout) })
}

func (p *boundedSSEScanner) Scan() bool {
	p.mu.Lock()
	if p.stopped || p.cause != nil {
		p.mu.Unlock()
		return false
	}
	p.idleGeneration++
	generation := p.idleGeneration
	if p.idleTimer != nil {
		p.idleTimer.Stop()
	}
	if p.idle > 0 {
		p.idleTimer = time.AfterFunc(p.idle, func() { p.expire(generation, true, errAnthropicNativeStreamIdle) })
	}
	p.mu.Unlock()
	ok := p.scanner.Scan()
	// CPU work and client writes between reads do not consume the idle budget.
	p.mu.Lock()
	p.idleGeneration++
	if p.idleTimer != nil {
		p.idleTimer.Stop()
	}
	failed := p.cause != nil
	p.mu.Unlock()
	return ok && !failed
}
func (p *boundedSSEScanner) Text() string { return p.scanner.Text() }
func (p *boundedSSEScanner) Err() error {
	p.mu.Lock()
	cause := p.cause
	p.mu.Unlock()
	if cause != nil {
		return cause
	}
	return p.scanner.Err()
}
func (p *boundedSSEScanner) closeBody() { p.closeOnce.Do(func() { _ = p.body.Close() }) }
func (p *boundedSSEScanner) Close() {
	p.mu.Lock()
	p.stopped = true
	p.idleGeneration++
	if p.idleTimer != nil {
		p.idleTimer.Stop()
	}
	if p.drainTimer != nil {
		p.drainTimer.Stop()
	}
	p.mu.Unlock()
	if p.stopContext != nil {
		p.stopContext()
	}
	p.closeBody()
}

func boundedGatewaySSEScanner(c *gin.Context, resp *http.Response, cfg *config.Config) *boundedSSEScanner {
	maxLine, interval := defaultMaxLineSize, time.Duration(0)
	if cfg != nil {
		if cfg.Gateway.MaxLineSize > 0 {
			maxLine = cfg.Gateway.MaxLineSize
		}
		if cfg.Gateway.StreamDataIntervalTimeout > 0 {
			interval = time.Duration(cfg.Gateway.StreamDataIntervalTimeout) * time.Second
		}
	}
	scanner := bufio.NewScanner(resp.Body)
	// Reserve one delimiter byte beyond the content limit, including when the
	// configured limit is smaller than bufio.Scanner's initial buffer.
	scannerMax := maxLine
	if scannerMax < int(^uint(0)>>1) {
		scannerMax++
	}
	scanner.Buffer(make([]byte, 0, min(64*1024, scannerMax)), scannerMax)
	scanner.Split(splitGatewaySSELines(maxLine))
	var ctx context.Context
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	return newBoundedSSEScanner(ctx, resp.Body, scanner, interval, upstreamCancelDrainTimeout)
}

// splitGatewaySSELines accepts all SSE line endings, including a CRLF split
// across Reads. A CR ends its line immediately; the next LF is consumed without
// manufacturing a blank event or waiting for another upstream byte after CR.
// The limit counts content bytes, not the CR/LF delimiter.
func splitGatewaySSELines(maxLine int) bufio.SplitFunc {
	skipLF := false
	return func(data []byte, atEOF bool) (int, []byte, error) {
		prefix := 0
		if skipLF && len(data) > 0 {
			skipLF = false
			if data[0] == '\n' {
				prefix = 1
			}
		}
		line := data[prefix:]
		for i, b := range line {
			if b == '\r' || b == '\n' {
				if i > maxLine {
					return 0, nil, bufio.ErrTooLong
				}
				skipLF = b == '\r'
				return prefix + i + 1, line[:i], nil
			}
		}
		if len(line) > maxLine {
			return 0, nil, bufio.ErrTooLong
		}
		if atEOF && len(line) > 0 {
			return len(data), line, nil
		}
		return prefix, nil, nil
	}
}

// Converted streams must decode one SSE event, not each data line. Keep the
// existing parser's field handling while distinguishing an empty data field
// (a dispatched event) from an event with no data fields (ignored by SSE).
type boundedGatewaySSEFrames struct {
	parser   openAICompatSSEFrameParser
	started  bool
	hasData  bool
	bytes    int
	maxBytes int
}

func newBoundedGatewaySSEFrames(cfg *config.Config) *boundedGatewaySSEFrames {
	maxBytes := defaultMaxLineSize
	if cfg != nil && cfg.Gateway.MaxLineSize > 0 {
		maxBytes = cfg.Gateway.MaxLineSize
	}
	return &boundedGatewaySSEFrames{maxBytes: maxBytes}
}

func (p *boundedGatewaySSEFrames) AddLine(line string) (openAICompatSSEFrame, bool, error) {
	if !p.started {
		line = strings.TrimPrefix(line, "\ufeff")
		p.started = true
	}
	if _, ok := extractOpenAISSEDataLine(line); ok {
		// Match the raw stream's aggregate data-frame bound. Comments and other
		// fields are not accumulated by the underlying parser.
		if len(line)+1 > p.maxBytes-p.bytes {
			return openAICompatSSEFrame{}, false, errors.New("upstream SSE frame exceeds configured maximum line size")
		}
		p.bytes += len(line) + 1
		p.hasData = true
	}
	frame, _ := p.parser.AddLine(line)
	if line != "" {
		return frame, false, nil
	}
	ready := p.hasData
	p.hasData, p.bytes = false, 0
	return frame, ready, nil
}

func (p *boundedGatewaySSEFrames) Finish() (openAICompatSSEFrame, bool) {
	// Preserve these adapters' existing EOF compatibility: a final data frame
	// can be consumed without a blank separator. Strict EventSource parsing
	// would discard it; callers must still surface any accompanying read error.
	frame, _ := p.parser.Finish()
	ready := p.hasData
	p.hasData, p.bytes = false, 0
	return frame, ready
}

func (p *boundedGatewaySSEFrames) standaloneDone() bool {
	return len(p.parser.dataLines) == 1 && strings.TrimSpace(p.parser.dataLines[0]) == "[DONE]"
}

// Retain the caller's total deadline and bound cancellation even while waiting
// for response headers. The release owner must live until response-body Close.
func newUpstreamDrainContext(parent context.Context, drain time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if deadline, ok := parent.Deadline(); ok {
		ctx, cancel = context.WithDeadline(context.WithoutCancel(parent), deadline)
	} else {
		ctx, cancel = context.WithCancel(context.WithoutCancel(parent))
	}
	var mu sync.Mutex
	var timer *time.Timer
	released := false
	stop := context.AfterFunc(parent, func() {
		mu.Lock()
		defer mu.Unlock()
		if !released {
			timer = time.AfterFunc(drain, cancel)
		}
	})
	return ctx, func() {
		mu.Lock()
		released = true
		if timer != nil {
			timer.Stop()
		}
		mu.Unlock()
		stop()
		cancel()
	}
}

type upstreamReleaseBody struct {
	io.ReadCloser
	release  context.CancelFunc
	once     sync.Once
	closeErr error
}

func (b *upstreamReleaseBody) Close() error {
	b.once.Do(func() { b.release(); b.closeErr = b.ReadCloser.Close() })
	return b.closeErr
}

// validCCFinishReason follows the Chat Completions wire enum. Unknown vendor
// failures (for example "error") must never be converted to a successful stop.
func validCCFinishReason(reason string) bool {
	switch reason {
	case "stop", "length", "tool_calls", "function_call", "content_filter":
		return true
	default:
		return false
	}
}

func gatewayClientDisconnected(c *gin.Context) bool {
	return c != nil && c.Request != nil && c.Request.Context().Err() != nil
}

func ccPayloadError(payload string) error {
	if !json.Valid([]byte(payload)) || !gjson.Parse(payload).IsObject() {
		return errors.New("invalid Chat Completions stream JSON")
	}
	if e := gjson.Get(payload, "error"); e.Exists() && e.Type != gjson.Null {
		return errors.New("upstream Chat Completions error event")
	}
	if gjson.Get(payload, "type").String() == "error" {
		return errors.New("upstream Chat Completions error event")
	}
	for _, choice := range gjson.Get(payload, "choices").Array() {
		reason := choice.Get("finish_reason")
		if reason.Exists() && reason.Type != gjson.Null &&
			(reason.Type != gjson.String || (reason.String() != "" && !validCCFinishReason(reason.String()))) {
			return errors.New("invalid upstream Chat Completions finish_reason")
		}
	}
	return nil
}

// scanAnthropicCCEvents is shared by both Anthropic-to-Chat adapters, including
// their buffered variants. Observed usage is consumed before returning failure;
// only a valid message_start followed by message_stop can finalize conversion.
func scanAnthropicCCEvents(c *gin.Context, resp *http.Response, cfg *config.Config, emit func(*apicompat.AnthropicStreamEvent, string) bool) error {
	scanner := boundedGatewaySSEScanner(c, resp, cfg)
	defer scanner.Close()
	frames := newBoundedGatewaySSEFrames(cfg)
	started := false
	processFrame := func(frame openAICompatSSEFrame) (bool, error) {
		payload := frame.Data
		// Preserve the adapter's existing tolerant protocol matching. The frame
		// retains its exact lexical event value for other consumers.
		eventType := strings.TrimSpace(frame.EventType)
		if eventType == "error" {
			return false, errors.New("upstream Anthropic error event")
		}
		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return false, fmt.Errorf("decode Anthropic SSE event: %w", err)
		}
		if event.Type == "error" || gjson.Get(payload, "error").IsObject() {
			return false, errors.New("upstream Anthropic error event")
		}
		if event.Type == "" || (eventType != "" && eventType != event.Type) {
			return false, errors.New("invalid Anthropic SSE event type")
		}
		if event.Type == "message_start" {
			if event.Message == nil || strings.TrimSpace(event.Message.ID) == "" || started {
				return false, errors.New("invalid Anthropic message_start")
			}
			started = true
		}
		if event.Type != "ping" && !started {
			return false, errors.New("upstream Anthropic event arrived before message_start")
		}
		if event.Type == "message_stop" {
			if err := scanner.Err(); err != nil {
				return false, err
			}
		}
		if emit(&event, payload) {
			scanner.startDrain()
		}
		return event.Type == "message_stop", nil
	}
	for scanner.Scan() {
		frame, ready, err := frames.AddLine(scanner.Text())
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		complete, err := processFrame(frame)
		if err != nil {
			return err
		}
		if complete {
			return nil
		}
	}
	complete := false
	if frame, ready := frames.Finish(); ready {
		var err error
		complete, err = processFrame(frame)
		if err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if complete {
		return nil
	}
	return errors.New("upstream Anthropic stream ended before message_stop")
}

func readAnthropicCCResponse(c *gin.Context, resp *http.Response, cfg *config.Config) (*apicompat.AnthropicResponse, ClaudeUsage, error) {
	var response *apicompat.AnthropicResponse
	var usage ClaudeUsage
	err := scanAnthropicCCEvents(c, resp, cfg, func(event *apicompat.AnthropicStreamEvent, _ string) bool {
		if event.Type == "message_start" && event.Message != nil {
			response = event.Message
			mergeAnthropicUsage(&usage, event.Message.Usage)
		}
		if event.Type == "message_delta" {
			if event.Usage != nil {
				mergeAnthropicUsage(&usage, *event.Usage)
			}
			if event.Delta != nil && event.Delta.StopReason != "" && response != nil {
				response.StopReason = apicompat.AnthropicStopReasonPtr(event.Delta.StopReason)
			}
		}
		if event.Type == "content_block_start" && event.ContentBlock != nil && response != nil {
			response.Content = append(response.Content, *event.ContentBlock)
		}
		if event.Type == "content_block_delta" && event.Delta != nil && response != nil && event.Index != nil {
			idx := *event.Index
			if idx >= 0 && idx < len(response.Content) {
				switch event.Delta.Type {
				case "text_delta":
					response.Content[idx].Text += event.Delta.Text
				case "thinking_delta":
					response.Content[idx].Thinking += event.Delta.Thinking
				case "input_json_delta":
					response.Content[idx].Input = appendRawJSON(response.Content[idx].Input, event.Delta.PartialJSON)
				}
			}
		}
		return false
	})
	return response, usage, err
}
