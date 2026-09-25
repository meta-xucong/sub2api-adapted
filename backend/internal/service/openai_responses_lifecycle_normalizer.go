package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// openAIResponsesLifecycleNormalizer repairs the one Responses lifecycle
// difference seen in otherwise valid third-party native streams: some
// upstreams emit response.created and then immediately start output events,
// omitting response.in_progress.  The downstream Codex contract expects the
// two start events in that order.
//
// The normalizer buffers at most the first two SSE frames.  If the upstream
// already emits response.in_progress, the bytes pass through unchanged.  If
// it does not, a synthetic in-progress frame is inserted and sequence numbers
// after response.created are shifted by one.  This keeps the stream both
// strict and compatible without waiting for the whole response.
type openAIResponsesLifecycleNormalizer struct {
	source       io.ReadCloser
	reader       *bufio.Reader
	output       []byte
	pendingError error
	initialized  bool
	passthrough  bool
	inserted     bool
	finished     bool
	closed       bool
}

func newOpenAIResponsesLifecycleNormalizer(source io.ReadCloser) io.ReadCloser {
	if source == nil {
		return source
	}
	return &openAIResponsesLifecycleNormalizer{
		source: source,
		reader: bufio.NewReaderSize(source, 8*1024),
	}
}

func (n *openAIResponsesLifecycleNormalizer) Read(p []byte) (int, error) {
	if n == nil || n.closed {
		return 0, io.ErrClosedPipe
	}
	for len(n.output) == 0 && !n.finished {
		if err := n.fillOutput(); err != nil {
			if err == io.EOF {
				n.finished = true
			}
			if len(n.output) == 0 {
				return 0, err
			}
		}
	}
	if len(n.output) == 0 {
		return 0, io.EOF
	}
	count := copy(p, n.output)
	n.output = n.output[count:]
	return count, nil
}

func (n *openAIResponsesLifecycleNormalizer) Close() error {
	if n == nil || n.closed {
		return nil
	}
	n.closed = true
	if n.source == nil {
		return nil
	}
	return n.source.Close()
}

func (n *openAIResponsesLifecycleNormalizer) fillOutput() error {
	if n.pendingError != nil {
		err := n.pendingError
		n.pendingError = nil
		return err
	}
	if n.passthrough {
		line, err := n.reader.ReadString('\n')
		if len(line) > 0 {
			n.output = append(n.output, line...)
			if err != nil {
				n.pendingError = err
			}
			return nil
		}
		return err
	}
	if !n.initialized {
		var firstFrame bytes.Buffer
		for {
			line, lineErr := n.reader.ReadString('\n')
			if len(line) > 0 {
				lineType := openAIResponsesSSELineType(line)
				if lineType != "" && lineType != "response.created" {
					n.initialized = true
					n.passthrough = true
					n.output = append(n.output, firstFrame.Bytes()...)
					n.output = append(n.output, line...)
					if lineErr != nil {
						n.pendingError = lineErr
					}
					return nil
				}
				_, _ = firstFrame.WriteString(line)
				if strings.TrimSpace(line) == "" {
					n.initialized = true
					first := firstFrame.Bytes()
					if openAIResponsesSSEFrameType(first) != "response.created" {
						n.passthrough = true
						n.output = append(n.output, first...)
						return nil
					}
					second, secondErr := n.readFrame()
					if secondErr == nil {
						if openAIResponsesSSEFrameType(second) == "response.in_progress" {
							n.passthrough = true
							n.output = append(n.output, first...)
							n.output = append(n.output, second...)
							return nil
						}
						n.inserted = true
						n.output = append(n.output, first...)
						n.output = append(n.output, openAIResponsesSyntheticInProgress(first)...)
						n.output = append(n.output, openAIResponsesShiftSequence(second, 1)...)
						return nil
					}
					if secondErr == io.EOF {
						n.inserted = true
						n.finished = true
						n.output = append(n.output, first...)
						n.output = append(n.output, openAIResponsesSyntheticInProgress(first)...)
						return nil
					}
					return secondErr
				}
			}
			if lineErr != nil {
				if firstFrame.Len() > 0 {
					n.initialized = true
					n.passthrough = true
					n.output = append(n.output, firstFrame.Bytes()...)
					n.pendingError = lineErr
					return nil
				}
				return lineErr
			}
		}
	}
	if !n.inserted {
		first, err := n.readFrame()
		if err != nil {
			return err
		}
		n.output = append(n.output, first...)
		return nil
	}
	first, err := n.readFrame()
	if err != nil {
		return err
	}
	if openAIResponsesSSEFrameType(first) == "response.in_progress" {
		// The synthetic start event is authoritative for this normalized stream.
		return nil
	}
	n.output = append(n.output, openAIResponsesShiftSequence(first, 1)...)
	return nil
}

func (n *openAIResponsesLifecycleNormalizer) readFrame() ([]byte, error) {
	if n.pendingError != nil {
		err := n.pendingError
		n.pendingError = nil
		return nil, err
	}
	var frame bytes.Buffer
	for {
		line, err := n.reader.ReadString('\n')
		if len(line) > 0 {
			_, _ = frame.WriteString(line)
			if strings.TrimSpace(line) == "" {
				if err != nil {
					n.pendingError = err
				}
				return frame.Bytes(), nil
			}
		}
		if err != nil {
			if frame.Len() > 0 {
				n.pendingError = err
				return frame.Bytes(), nil
			}
			return nil, err
		}
	}
}

func openAIResponsesSSEFrameType(frame []byte) string {
	eventType := ""
	data := ""
	for _, line := range strings.Split(string(frame), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if event, ok := extractOpenAISSEEventLine(line); ok {
			eventType = strings.TrimSpace(event)
		}
		if value, ok := extractOpenAISSEDataLine(line); ok {
			data += value
		}
	}
	if strings.TrimSpace(data) != "" && data != "[DONE]" {
		var payload struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(data), &payload) == nil && strings.TrimSpace(payload.Type) != "" {
			return strings.TrimSpace(payload.Type)
		}
	}
	return eventType
}

func openAIResponsesSSELineType(line string) string {
	if eventType, ok := extractOpenAISSEEventLine(strings.TrimSuffix(line, "\r\n")); ok {
		return strings.TrimSpace(eventType)
	}
	data, ok := extractOpenAISSEDataLine(strings.TrimSuffix(line, "\r\n"))
	if !ok {
		return ""
	}
	data = strings.TrimSpace(data)
	if data == "[DONE]" {
		return "[DONE]"
	}
	var payload struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(data), &payload) == nil {
		return strings.TrimSpace(payload.Type)
	}
	return ""
}

func openAIResponsesSyntheticInProgress(createdFrame []byte) []byte {
	data := ""
	for _, line := range strings.Split(string(createdFrame), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if value, ok := extractOpenAISSEDataLine(line); ok {
			data += value
		}
	}
	var payload map[string]any
	if json.Unmarshal([]byte(data), &payload) != nil {
		return []byte("event: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"sequence_number\":1}\n\n")
	}
	payload["type"] = "response.in_progress"
	sequence := 0
	if value, ok := payload["sequence_number"].(float64); ok {
		sequence = int(value)
	}
	payload["sequence_number"] = sequence + 1
	if response, ok := payload["response"].(map[string]any); ok {
		response["status"] = "in_progress"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return []byte("event: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"sequence_number\":1}\n\n")
	}
	return []byte("event: response.in_progress\ndata: " + string(encoded) + "\n\n")
}

func openAIResponsesShiftSequence(frame []byte, delta int) []byte {
	if delta == 0 || len(frame) == 0 {
		return frame
	}
	var output strings.Builder
	for _, rawLine := range strings.SplitAfter(string(frame), "\n") {
		line := strings.TrimSuffix(rawLine, "\n")
		ending := ""
		if strings.HasSuffix(line, "\r") {
			line = strings.TrimSuffix(line, "\r")
			ending = "\r"
		}
		if data, ok := extractOpenAISSEDataLine(line); ok && strings.TrimSpace(data) != "" && strings.TrimSpace(data) != "[DONE]" {
			var payload map[string]any
			if json.Unmarshal([]byte(data), &payload) == nil {
				if value, ok := payload["sequence_number"].(float64); ok {
					payload["sequence_number"] = int(value) + delta
					if encoded, marshalErr := json.Marshal(payload); marshalErr == nil {
						line = "data: " + string(encoded)
					}
				}
			}
		}
		output.WriteString(line)
		output.WriteString(ending)
		if strings.HasSuffix(rawLine, "\n") {
			output.WriteByte('\n')
		}
	}
	return []byte(output.String())
}
