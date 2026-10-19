// Package proxyurl 提供代理 URL 的统一验证（fail-fast，无效代理不回退直连）
//
// 所有需要解析代理 URL 的地方必须通过此包的安全解析入口。
// 直接使用 url.Parse 处理代理 URL 是被禁止的。
// 这确保了 fail-fast 行为：无效代理配置在创建时立即失败，
// 而不是在运行时静默回退到直连（产生 IP 关联风险）。
package proxyurl

import (
	"errors"
	"net/url"
	"strings"
)

// allowedSchemes 代理协议白名单
var allowedSchemes = map[string]bool{
	"http":    true,
	"https":   true,
	"socks5":  true,
	"socks5h": true,
}

// Parse 解析并验证代理 URL。
//
// 语义:
//   - 空字符串 → ("", nil, nil)，表示直连
//   - 非空且有效 → (trimmed, *url.URL, nil)
//   - 非空但无效 → ("", nil, error)，fail-fast 不回退
//
// 验证规则:
//   - TrimSpace 后为空视为直连
//   - url.Parse 失败返回 error（不含原始 URL，防凭据泄露）
//   - Host 为空返回安全分类 error（不含任何 URL 组成部分）
//   - Scheme 必须为 http/https/socks5/socks5h
//   - socks5:// 自动升级为 socks5h://（确保 DNS 由代理端解析，防止 DNS 泄漏）
func Parse(raw string) (trimmed string, parsed *url.URL, err error) {
	return parse(raw, true)
}

// ParsePreservingScheme applies the same fail-closed validation and safe errors as
// Parse, without rewriting socks5 to socks5h. Existing callers whose transport
// owns DNS semantics must use this entry point to preserve their routing policy.
func ParsePreservingScheme(raw string) (trimmed string, parsed *url.URL, err error) {
	return parse(raw, false)
}

var (
	ErrInvalidURL        = errors.New("invalid proxy URL")
	ErrMissingHost       = errors.New("proxy URL missing host")
	ErrUnsupportedScheme = errors.New("unsupported proxy scheme (allowed: http, https, socks5, socks5h)")
)

func parse(raw string, upgradeSOCKS bool) (trimmed string, parsed *url.URL, err error) {
	trimmed = strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil, nil
	}

	parsed, err = url.Parse(trimmed)
	if err != nil {
		// Neither wrap nor format url.Error: it retains the original URL, including secrets.
		return "", nil, ErrInvalidURL
	}

	if parsed.Host == "" || parsed.Hostname() == "" {
		return "", nil, ErrMissingHost
	}

	scheme := strings.ToLower(parsed.Scheme)
	if !allowedSchemes[scheme] {
		return "", nil, ErrUnsupportedScheme
	}

	// 保留 Parse 既有的 socks5 → socks5h 规范化策略。
	// DNS 行为取决于调用方的 transport/dialer；既有协议路径不应被无声改写。
	if upgradeSOCKS && scheme == "socks5" {
		parsed.Scheme = "socks5h"
		trimmed = parsed.String()
	}

	return trimmed, parsed, nil
}
