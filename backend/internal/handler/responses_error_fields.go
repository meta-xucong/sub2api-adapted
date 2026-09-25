package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Normalize only JSON error envelopes on Responses routes. Preserve HTTP
// status, existing provider codes and all unrelated response fields.
type responsesErrorFieldsWriter struct{ gin.ResponseWriter }

func (w *responsesErrorFieldsWriter) Write(body []byte) (int, error) {
	originalLen := len(body)
	if w.Status() >= 400 && strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") && gjson.ValidBytes(body) && gjson.GetBytes(body, "error").IsObject() {
		kind := gjson.GetBytes(body, "error.type").String()
		if kind == "" {
			kind = "invalid_request_error"
			if w.Status() >= 500 {
				kind = "server_error"
			}
			if p, e := sjson.SetBytes(body, "error.type", kind); e == nil {
				body = p
			}
		}
		if gjson.GetBytes(body, "error.code").String() == "" {
			if p, e := sjson.SetBytes(body, "error.code", kind); e == nil {
				body = p
			}
		}
		w.Header().Del("Content-Length")
	}
	n, err := w.ResponseWriter.Write(body)
	if err != nil {
		return n, err
	}
	return originalLen, nil
}
func (w *responsesErrorFieldsWriter) WriteString(body string) (int, error) {
	return w.Write([]byte(body))
}
