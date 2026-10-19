package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// openAIRawStreamTruncatedUpstreamMessage 是 raw CC 直转路径上游截断的 ops 消息。
const openAIRawStreamTruncatedUpstreamMessage = "Upstream Chat Completions stream ended before any terminal chunk"

// openAIRawStreamTerminalState 记录 raw Chat Completions SSE 流是否收到过
// **终止信号**。
//
// 背景：CC 直转路径把上游 SSE 原样透传，此前只要 HTTP 状态是 200 就按成功收尾——
// 上游中途断流（Cloudflare edge reset、后端 worker 掉线）会被伪装成
// `HTTP 200 + usage 0/0`：客户端拿到半截回答，网关既不报错也不计入 SLA，
// Ops 侧完全不可见。
//
// [DONE] or valid finish reasons for every observed choice establish completion. Usage is observed
// independently: providers can report interim usage while generation is still
// running, so usage alone must never turn a partial EOF into successful output.
type openAIRawStreamTerminalState struct {
	// sawDataLine 表示上游至少发过一行 `data:`，即响应确实是 SSE 语义流。
	// 非 SSE 响应体（上游对 stream 请求返回裸 JSON）不参与截断判定，保持既有透传行为。
	sawDataLine     bool
	sawDone         bool
	sawFinishReason bool
	choices         map[int64]bool
	// The outbound request, rather than upstream-supplied indexes, bounds state.
	// A zero value retains the protocol's default single-choice behavior.
	expectedChoices   int64
	unfinishedChoices int
}

func rawChatCompletionsRequestedChoices(body []byte) int64 {
	n := gjson.GetBytes(body, "n")
	if n.Type == gjson.Number {
		if count, err := strconv.ParseInt(n.Raw, 10, 64); err == nil && count > 0 {
			return count
		}
	}
	return 1
}

// ObserveDataLine 从单行 SSE `data:` 载荷中提取终止信号。payload 需已 TrimSpace。
func (t *openAIRawStreamTerminalState) ObserveDataLine(payload string) error {
	if t == nil {
		return nil
	}
	t.sawDataLine = true
	if payload == "[DONE]" {
		t.sawDone = true
		return nil
	}
	limit := t.expectedChoices
	if limit <= 0 {
		limit = 1
	}
	for _, choice := range gjson.Get(payload, "choices").Array() {
		// Preserve omitted-index compatibility as choice zero, but do not let
		// malformed or out-of-request indexes allocate terminal state.
		idx := int64(0)
		if index := choice.Get("index"); index.Exists() {
			var err error
			idx, err = strconv.ParseInt(index.Raw, 10, 64)
			if index.Type != gjson.Number || err != nil {
				return errors.New("invalid upstream Chat Completions choice index")
			}
		}
		if idx < 0 || idx >= limit {
			return errors.New("upstream Chat Completions choice index exceeds requested n")
		}
		if t.choices == nil {
			t.choices = make(map[int64]bool)
		}
		finished := validCCFinishReason(choice.Get("finish_reason").String())
		previouslyFinished, observed := t.choices[idx]
		if !observed {
			t.choices[idx] = finished
			if !finished {
				t.unfinishedChoices++
			}
		} else if !previouslyFinished && finished {
			t.choices[idx] = true
			t.unfinishedChoices--
		}
	}
	t.sawFinishReason = len(t.choices) > 0 && t.unfinishedChoices == 0
	return nil
}

// Terminated 表示上游给出过终止信号。
func (t *openAIRawStreamTerminalState) Terminated() bool {
	// A global sentinel is sufficient for ordinary single-choice streams. For
	// multi-choice streams, one finished choice must not hide another truncation.
	return t != nil && (t.sawFinishReason || (t.sawDone && len(t.choices) <= 1))
}

// IsTruncated 判定上游是否在任何终止信号之前结束。clientOutputStarted 用于放行
// 非 SSE 响应体：那类响应本就没有 data: 行，既有行为是原样透传，不在本次判定范围内；
// 但"一个字节都没收到"的空 200 依然算截断。
func (t *openAIRawStreamTerminalState) IsTruncated(clientOutputStarted bool) bool {
	if t == nil || t.Terminated() {
		return false
	}
	return t.sawDataLine || !clientOutputStarted
}

// newOpenAIRawStreamTruncatedFailoverError 处理"上游截断且尚未向客户端写出任何
// 字节"的情况：响应头还没提交，可以透明换号重试，客户端不会看到半截流。
func newOpenAIRawStreamTruncatedFailoverError(
	c *gin.Context,
	account *Account,
	upstreamRequestID string,
	cause error,
) *UpstreamFailoverError {
	recordOpenAIRawStreamTruncation(c, account, upstreamRequestID, cause, "failover")

	headers := http.Header{}
	if id := strings.TrimSpace(upstreamRequestID); id != "" {
		headers.Set("x-request-id", id)
	}
	return &UpstreamFailoverError{
		StatusCode:      http.StatusBadGateway,
		ResponseBody:    openAIRawStreamTruncatedErrorBody(cause),
		ResponseHeaders: headers,
	}
}

// recordOpenAIRawStreamTruncation 把上游截断记入 ops 上下文，使其在错误日志与
// 账号健康度中可见——这正是此前"HTTP 200 假成功"丢掉的信息。
func recordOpenAIRawStreamTruncation(
	c *gin.Context,
	account *Account,
	upstreamRequestID string,
	cause error,
	kind string,
) {
	if c == nil {
		return
	}
	message := openAIRawStreamTruncatedMessage(cause)
	platform := PlatformOpenAI
	accountID := int64(0)
	accountName := ""
	if account != nil {
		platform = account.Platform
		accountID = account.ID
		accountName = account.Name
	}

	setOpsUpstreamError(c, http.StatusBadGateway, message, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		ProxyID:            opsUpstreamProxyID(account),
		ProxyName:          opsUpstreamProxyName(account),
		Platform:           platform,
		AccountID:          accountID,
		AccountName:        accountName,
		UpstreamStatusCode: http.StatusBadGateway,
		UpstreamRequestID:  strings.TrimSpace(upstreamRequestID),
		Kind:               kind,
		Message:            message,
	})
}

// openAIRawStreamTruncatedMessage 拼出 ops 消息：干净 EOF 没有底层错误可带，
// 传输层错误（connection reset / http2 stream error）则保留原因以便定位。
func openAIRawStreamTruncatedMessage(cause error) string {
	if cause == nil || errors.Is(cause, ErrOpenAIUpstreamStreamTruncated) {
		return openAIRawStreamTruncatedUpstreamMessage
	}
	return openAIRawStreamTruncatedUpstreamMessage + ": " + cause.Error()
}

// openAIRawStreamTruncatedErrorBody 构造 failover 错误体，code/message 与
// 写出后走 openAIUpstreamStreamReadError 的客户端分类保持一致。
func openAIRawStreamTruncatedErrorBody(cause error) []byte {
	code, message := classifyOpenAIUpstreamStreamReadError(cause)
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    "upstream_error",
			"code":    code,
			"message": message,
		},
	})
	if err != nil {
		return []byte(`{"error":{"type":"upstream_error","code":"` + OpenAIUpstreamStreamTruncatedCode +
			`","message":"Upstream response stream ended before completion"}}`)
	}
	return body
}
