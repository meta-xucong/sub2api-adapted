package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/nativeharness"
	"github.com/gin-gonic/gin"
	"net/http"
	"time"
)

func (h *OpenAIGatewayHandler) nativeHarnessEvents(c *gin.Context, userID, keyID int64, key string, canExecute bool, raw []byte) {
	protocol, protocolErr := nativeharness.StreamProtocol(c.Request.Header.Values("X-Native-Stream-Protocol"), true)
	if protocolErr != nil {
		nativeHarnessError(c, 400, "INVALID_STREAM_PROTOCOL", "Unsupported native stream protocol")
		return
	}
	standard := protocol == nativeharness.StandardStreamProtocol
	cursor := int64(0)
	headers := c.Request.Header.Values("Last-Event-ID")
	resume := len(headers) != 0
	if resume {
		if len(headers) != 1 {
			nativeHarnessError(c, 400, "INVALID_EVENT_CURSOR", "One Last-Event-ID is required")
			return
		}
		var err error
		cursor, err = nativeharness.ParseEventCursor(headers[0])
		if err != nil {
			nativeHarnessError(c, 400, "INVALID_EVENT_CURSOR", "Invalid Last-Event-ID")
			return
		}
	}
	if standard && cursor > 0 {
		nativeHarnessError(c, 400, "STANDARD_STREAM_CURSOR_UNSUPPORTED", "Use Last-Event-ID:0 or the native-events-v1 protocol")
		return
	}
	model, modelErr := nativeharness.RequestModel(raw)
	if modelErr != nil {
		nativeHarnessError(c, 400, "INVALID_REQUEST", "Invalid native request")
		return
	}
	projector := nativeharness.NewStandardStream(model, time.Now().Unix())
	if h.nativeHarness.Config.StandardWaitSeconds <= 0 || h.nativeHarness.Config.StandardWaitSeconds > 300 {
		nativeHarnessError(c, 503, "NATIVE_WAIT_CONFIG_INVALID", "Streaming observation requires a positive bounded budget")
		return
	}
	// Gin implements Flush() without returning its underlying error. Unwrap it
	// so ResponseController can use net/http's FlushError and write deadlines.
	var transport http.ResponseWriter = c.Writer
	for i := 0; i < 8; i++ {
		wrapper, ok := transport.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		transport = wrapper.Unwrap()
	}
	controller := http.NewResponseController(transport)
	totalDeadline := time.Now().Add(time.Duration(h.nativeHarness.Config.StandardWaitSeconds) * time.Second)
	if deadline, ok := c.Request.Context().Deadline(); ok && deadline.Before(totalDeadline) {
		totalDeadline = deadline
	}
	writeDeadline := func() error {
		until := time.Now().Add(5 * time.Second)
		if totalDeadline.Before(until) {
			until = totalDeadline
		}
		return controller.SetWriteDeadline(until)
	}
	if err := writeDeadline(); err != nil {
		nativeHarnessError(c, 503, "NATIVE_STREAM_TRANSPORT_UNAVAILABLE", "Bounded streaming transport required")
		return
	}
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	started := false
	send := func(typ string, data any, id *int64) error {
		body, err := json.Marshal(data)
		if err != nil {
			return err
		}
		frame := ""
		if id != nil {
			frame = fmt.Sprintf("id: %d\n", *id)
		}
		if typ != "" {
			frame += "event: " + typ + "\n"
		}
		frame += "data: " + string(body) + "\n\n"
		if err = writeDeadline(); err != nil {
			return err
		}
		if _, err = c.Writer.Write([]byte(frame)); err != nil {
			return err
		}
		return controller.Flush()
	}
	err := h.nativeHarness.ObserveEvents(c.Request.Context(), userID, keyID, key, canExecute, raw, cursor, resume, func(page *nativeharness.EventPage) error {
		if !started {
			c.Header("Content-Type", "text/event-stream; charset=utf-8")
			c.Header("Cache-Control", "no-store")
			c.Header("X-Accel-Buffering", "no")
			c.Header("X-Native-Stream-Mode", protocol)
			c.Header("X-Native-Usage-State", "unknown")
			c.Header("X-Native-Billing", "disabled-native-pilot")
			c.Status(http.StatusOK)
			started = true
		}
		for _, event := range page.Events {
			if standard {
				frames, done, err := projector.Project(page.TaskID, event)
				if err != nil {
					return err
				}
				for _, frame := range frames {
					if err = send("", frame, nil); err != nil {
						return err
					}
				}
				if done {
					if err = writeDeadline(); err != nil {
						return err
					}
					if _, err = c.Writer.Write([]byte("data: [DONE]\n\n")); err != nil {
						return err
					}
					if err = controller.Flush(); err != nil {
						return err
					}
				} else if len(frames) == 0 {
					// This extension is a comment, not assistant content. Generic
					// SDKs may ignore it; only native-aware clients show status.
					if event.Type == "native_status" || event.Type == "task_status" {
						body, err := json.Marshal(event.Data)
						if err != nil {
							return err
						}
						if err = writeDeadline(); err != nil {
							return err
						}
						if _, err = c.Writer.Write([]byte(": " + event.Type + " " + string(body) + "\n\n")); err != nil {
							return err
						}
						if err = controller.Flush(); err != nil {
							return err
						}
					}
				}
				continue
			}
			if err := send(event.Type, event.Data, &event.Seq); err != nil {
				return err
			}
		}
		// Comments keep the HTTP observation alive, never claim native progress.
		if len(page.Events) == 0 {
			if err := writeDeadline(); err != nil {
				return err
			}
			if _, err := c.Writer.Write([]byte(": observing\n\n")); err != nil {
				return err
			}
			return controller.Flush()
		}
		return nil
	})
	if c.Request.Context().Err() != nil {
		return
	}
	if err != nil {
		code, status := "NATIVE_UNAVAILABLE", 503
		var e *nativeharness.Error
		if errors.As(err, &e) {
			code, status = e.Code, e.Status
		}
		if !started {
			nativeHarnessError(c, status, code, "Native observation unavailable")
			return
		}
		// No cursor and no success marker: the client resumes its last durable ID.
		if standard {
			_ = send("", gin.H{"error": gin.H{"code": code, "type": "native_channel_error", "message": "Native stream interrupted; recover the same operation without new execution"}}, nil)
		} else {
			_ = send("observation_error", gin.H{"code": code, "status": status, "task_cancelled": false}, nil)
		}
		return
	}
	if !standard {
		_ = send("observation_end", gin.H{"reason": "terminal_cursor_reached"}, nil)
	}
}
