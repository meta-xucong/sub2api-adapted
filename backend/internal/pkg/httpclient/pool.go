// Package httpclient 提供共享 HTTP 客户端池
//
// 性能优化说明：
// 原实现在多个服务中重复创建 http.Client：
// 1. proxy_probe_service.go: 每次探测创建新客户端
// 2. pricing_service.go: 每次请求创建新客户端
// 3. turnstile_service.go: 每次验证创建新客户端
// 4. github_release_service.go: 每次请求创建新客户端
// 5. claude_usage_service.go: 每次请求创建新客户端
//
// 新实现使用统一的客户端池：
// 1. 相同配置复用同一 http.Client 实例
// 2. 复用 Transport 连接池，减少 TCP/TLS 握手开销
// 3. 支持 HTTP/HTTPS/SOCKS5/SOCKS5H 代理
// 4. 代理配置失败时直接返回错误，不会回退到直连（避免 IP 关联风险）
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

// Transport 连接池默认配置
const (
	defaultMaxIdleConns        = 100              // 最大空闲连接数
	defaultMaxIdleConnsPerHost = 10               // 每个主机最大空闲连接数
	defaultIdleConnTimeout     = 90 * time.Second // 空闲连接超时时间（建议小于上游 LB 超时）
	defaultDialTimeout         = 5 * time.Second  // TCP 连接超时（含代理握手），代理不通时快速失败
	defaultTLSHandshakeTimeout = 5 * time.Second  // TLS 握手超时
	validatedHostTTL           = 30 * time.Second // 仅用于可信代理路径的主机预检查缓存
)

// Options 定义共享 HTTP 客户端的构建参数
type Options struct {
	ProxyURL              string        // 代理 URL（支持 http/https/socks5/socks5h）
	Timeout               time.Duration // 请求总超时时间
	ResponseHeaderTimeout time.Duration // 等待响应头超时时间
	InsecureSkipVerify    bool          // 是否跳过 TLS 证书验证（已禁用，不允许设置为 true）
	ValidateResolvedIP    bool          // 直连时校验并绑定实际拨号 IP；代理路径仅做主机预检查
	// RequirePinnedDestination is required for untrusted download URLs. It implies
	// public-IP validation and fails closed for proxies, whose destination DNS
	// cannot be bound by the current transport. It conflicts with AllowPrivateHosts.
	RequirePinnedDestination bool
	AllowPrivateHosts        bool // 允许私有地址解析（与 ValidateResolvedIP 一起使用）

	// 可选的连接池参数（不设置则使用默认值）
	MaxIdleConns        int // 最大空闲连接总数（默认 100）
	MaxIdleConnsPerHost int // 每主机最大空闲连接（默认 10）
	MaxConnsPerHost     int // 每主机最大连接数（默认 0 无限制）
}

// sharedClients 存储按配置参数缓存的 http.Client 实例
var sharedClients sync.Map

// 允许测试替换校验函数，生产默认指向真实实现。
var validateResolvedIP = urlvalidator.ValidateResolvedIP

// GetClient 返回共享的 HTTP 客户端实例
// 性能优化：相同配置复用同一客户端，避免重复创建 Transport
// 安全说明：代理配置失败时直接返回错误，不会回退到直连，避免 IP 关联风险
func GetClient(opts Options) (*http.Client, error) {
	key := buildClientKey(opts)
	if cached, ok := sharedClients.Load(key); ok {
		if client, ok := cached.(*http.Client); ok {
			return client, nil
		}
	}

	client, err := buildClient(opts)
	if err != nil {
		return nil, err
	}

	actual, _ := sharedClients.LoadOrStore(key, client)
	if c, ok := actual.(*http.Client); ok {
		return c, nil
	}
	return client, nil
}

func buildClient(opts Options) (*http.Client, error) {
	transport, err := buildTransport(opts)
	if err != nil {
		return nil, err
	}

	var rt http.RoundTripper = transport
	if opts.ValidateResolvedIP && !opts.AllowPrivateHosts && strings.TrimSpace(opts.ProxyURL) != "" {
		// Preserve the existing precheck for trusted, fixed-origin proxy callers.
		// This is NOT DNS-rebinding protection: the proxy resolves the target itself.
		// Untrusted downloaders must set RequirePinnedDestination instead.
		rt = newValidatedTransport(transport)
	}
	rt = servertiming.WrapRoundTripper(rt)
	return &http.Client{
		Transport: rt,
		Timeout:   opts.Timeout,
	}, nil
}

func buildTransport(opts Options) (*http.Transport, error) {
	if opts.RequirePinnedDestination && opts.AllowPrivateHosts {
		return nil, errors.New("require_pinned_destination does not allow private hosts")
	}
	if opts.RequirePinnedDestination && strings.TrimSpace(opts.ProxyURL) != "" {
		// Refuse before parsing: even a malformed proxy URL may contain secrets.
		return nil, errors.New("require_pinned_destination cannot enforce destination IP validation through a proxy")
	}
	// 使用自定义值或默认值
	maxIdleConns := opts.MaxIdleConns
	if maxIdleConns <= 0 {
		maxIdleConns = defaultMaxIdleConns
	}
	maxIdleConnsPerHost := opts.MaxIdleConnsPerHost
	if maxIdleConnsPerHost <= 0 {
		maxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: defaultDialTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		MaxConnsPerHost:       opts.MaxConnsPerHost, // 0 表示无限制
		IdleConnTimeout:       defaultIdleConnTimeout,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
	}

	if opts.InsecureSkipVerify {
		// 安全要求：禁止跳过证书验证，避免中间人攻击。
		return nil, fmt.Errorf("insecure_skip_verify is not allowed; install a trusted certificate instead")
	}

	_, parsed, err := proxyurl.Parse(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	if parsed == nil {
		if opts.RequirePinnedDestination || (opts.ValidateResolvedIP && !opts.AllowPrivateHosts) {
			transport.DialContext = newValidatedDialer().DialContext
		}
		return transport, nil
	}
	if err := proxyutil.ConfigureTransportProxy(transport, parsed); err != nil {
		return nil, err
	}

	return transport, nil
}

func buildClientKey(opts Options) string {
	return fmt.Sprintf("%s|%s|%s|%t|%t|%t|%t|%d|%d|%d",
		strings.TrimSpace(opts.ProxyURL),
		opts.Timeout.String(),
		opts.ResponseHeaderTimeout.String(),
		opts.InsecureSkipVerify,
		opts.ValidateResolvedIP,
		opts.AllowPrivateHosts,
		opts.RequirePinnedDestination,
		opts.MaxIdleConns,
		opts.MaxIdleConnsPerHost,
		opts.MaxConnsPerHost,
	)
}

// validatedDialer resolves once per new connection and passes only validated
// numeric IP addresses to the socket dialer. The transport's original URL/Host
// remains untouched, preserving the original TLS SNI and certificate hostname.
// Reusing an established connection is safe; a fresh connection resolves again.
type validatedDialer struct {
	lookupIP    func(context.Context, string, string) ([]net.IP, error)
	dialContext func(context.Context, string, string) (net.Conn, error)
}

func newValidatedDialer() *validatedDialer {
	return &validatedDialer{
		lookupIP:    net.DefaultResolver.LookupIP,
		dialContext: (&net.Dialer{Timeout: defaultDialTimeout}).DialContext,
	}
}

func (d *validatedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultDialTimeout)
	defer cancel()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid destination address: %w", err)
	}
	if urlvalidator.IsBlockedHost(host) {
		return nil, errors.New("destination host is not allowed")
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		ips, err = d.lookupIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("dns resolution failed: %w", err)
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("dns resolution returned no addresses")
	}
	// Reject the whole answer set before dialing any address. A mixed public /
	// private answer must not turn into a private fallback after a failed dial.
	for _, ip := range ips {
		if !isPublicDestinationIP(ip) {
			return nil, fmt.Errorf("resolved ip %s is not allowed", ip)
		}
	}
	var dialErr error
	for i, ip := range ips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Reserve time for later validated candidates. A blackholed first IP
		// must not consume the entire DNS-and-dial budget and prevent fallback.
		deadline, _ := ctx.Deadline() // The outer timeout always supplies one.
		budget := time.Until(deadline) / time.Duration(len(ips)-i)
		dialCtx, cancelDial := context.WithTimeout(ctx, budget)
		conn, err := d.dialContext(dialCtx, network, net.JoinHostPort(ip.String(), port))
		cancelDial()
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	return nil, fmt.Errorf("dial validated destination: %w", dialErr)
}

// IsGlobalUnicast/IsPrivate alone do not exclude shared address space (including
// cloud metadata at 100.100.100.200), documentation, benchmarking, or reserved
// addresses. Keep strict download policy conservative for special-purpose blocks.
// References: IANA IPv4/IPv6 Special-Purpose Address Registries (2026-10-17).
var nonPublicDestinationPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	// Reject protocol-assignment space wholesale, including Teredo. A small
	// number of special anycasts here are deliberately not download targets.
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), // 6to4 may embed a private IPv4 destination.
	netip.MustParsePrefix("3fff::/20"),
}

var publicDestinationIPv6Prefix = netip.MustParsePrefix("2000::/3")

func isPublicDestinationIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap() // Apply the IPv4 policy to IPv4-mapped IPv6 too.
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return false
	}
	// Limit native IPv6 to global unicast allocation space. This also rejects
	// IPv4-compatible/translated addresses, both standard NAT64 ranges,
	// discard-only, deprecated site-local, and local SRv6 ranges. Standard
	// NAT64 is intentionally unsupported: pinning its IPv6 address alone does
	// not guarantee that the translated IPv4 destination is public.
	if address.Is6() && !publicDestinationIPv6Prefix.Contains(address) {
		return false
	}
	for _, prefix := range nonPublicDestinationPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

// validatedTransport is a legacy host precheck for trusted proxy origins only.
// It cannot constrain which IP a proxy actually connects to.
type validatedTransport struct {
	base           http.RoundTripper
	validatedHosts sync.Map // map[string]time.Time, value 为过期时间
	now            func() time.Time
}

func newValidatedTransport(base http.RoundTripper) *validatedTransport {
	return &validatedTransport{
		base: base,
		now:  time.Now,
	}
}

func (t *validatedTransport) isValidatedHost(host string, now time.Time) bool {
	if t == nil {
		return false
	}
	raw, ok := t.validatedHosts.Load(host)
	if !ok {
		return false
	}
	expireAt, ok := raw.(time.Time)
	if !ok {
		t.validatedHosts.Delete(host)
		return false
	}
	if now.Before(expireAt) {
		return true
	}
	t.validatedHosts.Delete(host)
	return false
}

func (t *validatedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req != nil && req.URL != nil {
		host := strings.ToLower(strings.TrimSpace(req.URL.Hostname()))
		if host != "" {
			now := time.Now()
			if t != nil && t.now != nil {
				now = t.now()
			}
			if !t.isValidatedHost(host, now) {
				if err := validateResolvedIP(host); err != nil {
					return nil, err
				}
				t.validatedHosts.Store(host, now.Add(validatedHostTTL))
			}
		}
	}
	if t == nil || t.base == nil {
		return nil, fmt.Errorf("validated transport base is nil")
	}
	return t.base.RoundTrip(req)
}
