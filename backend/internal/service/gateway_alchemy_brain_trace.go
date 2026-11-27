package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

func logAlchemyBrainSchemaForward(c *gin.Context, wireBody []byte) {
	if c == nil || c.Request == nil || c.Request.Header.Get("X-Alchemy-Brain-Schema-Trace") != "1" {
		return
	}
	id, err := uuid.Parse(c.Request.Header.Get("X-Client-Request-Id"))
	if err != nil {
		return
	}
	slog.InfoContext(c.Request.Context(), "alchemy_brain_schema_forward",
		"client_request_id", id.String(),
		"schema_sha256", alchemyBrainSchemaFingerprint(wireBody))
}

// Only a digest of the schema is logged. An empty digest means the forwarder
// has no usable structured-output schema on the wire.
func alchemyBrainSchemaFingerprint(wireBody []byte) string {
	format := gjson.GetBytes(wireBody, "output_config.format")
	schema := format.Get("schema")
	if format.Get("type").String() != "json_schema" || !schema.IsObject() || len(schema.Raw) > 64*1024 {
		return ""
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(schema.Raw), &value); err != nil {
		return ""
	}
	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(bytes.TrimSuffix(canonical.Bytes(), []byte("\n"))))
}
