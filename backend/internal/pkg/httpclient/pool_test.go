package httpclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestValidatedTransport_CacheHostValidation(t *testing.T) {
	originalValidate := validateResolvedIP
	defer func() { validateResolvedIP = originalValidate }()

	var validateCalls int32
	validateResolvedIP = func(host string) error {
		atomic.AddInt32(&validateCalls, 1)
		require.Equal(t, "api.openai.com", host)
		return nil
	}

	var baseCalls int32
	base := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		atomic.AddInt32(&baseCalls, 1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})

	now := time.Unix(1730000000, 0)
	transport := newValidatedTransport(base)
	transport.now = func() time.Time { return now }

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/responses", nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	require.Equal(t, int32(1), atomic.LoadInt32(&validateCalls))
	require.Equal(t, int32(2), atomic.LoadInt32(&baseCalls))
}

func TestValidatedTransport_ExpiredCacheTriggersRevalidation(t *testing.T) {
	originalValidate := validateResolvedIP
	defer func() { validateResolvedIP = originalValidate }()

	var validateCalls int32
	validateResolvedIP = func(_ string) error {
		atomic.AddInt32(&validateCalls, 1)
		return nil
	}

	base := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})

	now := time.Unix(1730001000, 0)
	transport := newValidatedTransport(base)
	transport.now = func() time.Time { return now }

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/responses", nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	now = now.Add(validatedHostTTL + time.Second)
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	require.Equal(t, int32(2), atomic.LoadInt32(&validateCalls))
}

func TestValidatedTransport_ValidationErrorStopsRoundTrip(t *testing.T) {
	originalValidate := validateResolvedIP
	defer func() { validateResolvedIP = originalValidate }()

	expectedErr := errors.New("dns rebinding rejected")
	validateResolvedIP = func(_ string) error {
		return expectedErr
	}

	var baseCalls int32
	base := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		atomic.AddInt32(&baseCalls, 1)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})

	transport := newValidatedTransport(base)
	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/responses", nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.ErrorIs(t, err, expectedErr)
	require.Equal(t, int32(0), atomic.LoadInt32(&baseCalls))
}

func TestValidatedDialerPinsCheckedIPAndRechecksNewConnections(t *testing.T) {
	lookups, dials := 0, 0
	d := &validatedDialer{
		lookupIP: func(_ context.Context, network, host string) ([]net.IP, error) {
			lookups++
			require.Equal(t, "ip", network)
			require.Equal(t, "rebind.invalid", host)
			if lookups > 1 {
				return []net.IP{net.ParseIP("127.0.0.1")}, nil
			}
			return []net.IP{net.ParseIP("8.8.8.8")}, nil
		},
		dialContext: func(_ context.Context, network, address string) (net.Conn, error) {
			dials++
			require.Equal(t, "tcp", network)
			require.Equal(t, "8.8.8.8:443", address, "socket dial must never resolve the hostname again")
			client, server := net.Pipe()
			_ = server.Close()
			return client, nil
		},
	}
	conn, err := d.DialContext(context.Background(), "tcp", "rebind.invalid:443")
	require.NoError(t, err)
	_ = conn.Close()
	_, err = d.DialContext(context.Background(), "tcp", "rebind.invalid:443")
	require.ErrorContains(t, err, "not allowed")
	require.Equal(t, 2, lookups)
	require.Equal(t, 1, dials)
}

func TestValidatedDialerRejectsUnsafeAnswersBeforeDial(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "0.0.0.0", "224.0.0.1", "::1", "fc00::1", "fe80::1", "::", "ff02::1", "::ffff:127.0.0.1", "0.1.2.3", "100.64.0.1", "100.100.100.200", "100.127.255.254", "192.0.0.1", "192.0.2.1", "192.88.99.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1", "::ffff:100.100.100.200", "::ffff:198.18.0.1", "::127.0.0.1", "64:ff9b::a9fe:a9fe", "64:ff9b::6464:64c8", "64:ff9b:1::1", "100::1", "100:0:0:1::1", "2001::1", "2001:2::1", "2001:db8::1", "2002:7f00:1::", "3fff::1", "5f00::1", "fec0::1"} {
		t.Run(address, func(t *testing.T) {
			d := &validatedDialer{
				lookupIP: func(context.Context, string, string) ([]net.IP, error) {
					return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP(address)}, nil
				},
				dialContext: func(context.Context, string, string) (net.Conn, error) {
					t.Fatal("unsafe DNS answer must not reach the dialer")
					return nil, nil
				},
			}
			_, err := d.DialContext(context.Background(), "tcp", "mixed.invalid:80")
			require.ErrorContains(t, err, "not allowed")
			_, err = d.DialContext(context.Background(), "tcp", net.JoinHostPort(address, "80"))
			require.ErrorContains(t, err, "not allowed")
		})
	}
}

func TestValidatedDialerEmptyAnswersAndCancellation(t *testing.T) {
	d := &validatedDialer{
		lookupIP: func(ctx context.Context, _, _ string) ([]net.IP, error) {
			return nil, ctx.Err()
		},
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("must not dial an empty or cancelled DNS result")
			return nil, nil
		},
	}
	_, err := d.DialContext(context.Background(), "tcp", "empty.invalid:80")
	require.ErrorContains(t, err, "no addresses")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = d.DialContext(ctx, "tcp", "empty.invalid:80")
	require.ErrorIs(t, err, context.Canceled)
}

func TestValidatedDialerReservesBudgetForLaterAddress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	dials := 0
	d := &validatedDialer{
		lookupIP: func(context.Context, string, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("1.1.1.1")}, nil
		},
		dialContext: func(dialCtx context.Context, _, address string) (net.Conn, error) {
			dials++
			candidateDeadline, ok := dialCtx.Deadline()
			require.True(t, ok)
			require.False(t, candidateDeadline.After(deadline))
			if dials == 1 {
				require.Equal(t, "8.8.8.8:443", address)
				require.True(t, candidateDeadline.Before(deadline))
				<-dialCtx.Done() // Simulate a blackholed first candidate.
				require.NoError(t, ctx.Err(), "the first candidate must leave time for fallback")
				return nil, dialCtx.Err()
			}
			require.Equal(t, "1.1.1.1:443", address)
			require.NoError(t, dialCtx.Err())
			client, server := net.Pipe()
			_ = server.Close()
			return client, nil
		},
	}
	conn, err := d.DialContext(ctx, "tcp", "fallback.invalid:443")
	require.NoError(t, err)
	_ = conn.Close()
	require.Equal(t, 2, dials)
}

func TestValidatedDialerPreservesTLSHostnameAndHost(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Observed-Host", r.Host)
		w.Header().Set("Observed-SNI", r.TLS.ServerName)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	name := server.Certificate().DNSNames[0]
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport, err := buildTransport(Options{RequirePinnedDestination: true})
	require.NoError(t, err)
	defer transport.CloseIdleConnections()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	d := &validatedDialer{
		lookupIP: func(context.Context, string, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("8.8.8.8")}, nil
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			require.Equal(t, net.JoinHostPort("8.8.8.8", port), address)
			// Test-only socket mapping keeps all traffic on loopback.
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	}
	transport.DialContext = d.DialContext
	client := &http.Client{Transport: transport}
	resp, err := client.Get("https://" + net.JoinHostPort(name, port) + "/image")
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, net.JoinHostPort(name, port), resp.Header.Get("Observed-Host"))
	require.Equal(t, name, resp.Header.Get("Observed-SNI"))
	_, err = client.Get("https://" + net.JoinHostPort("wrong.invalid", port))
	require.Error(t, err, "the original hostname must still be certificate-verified")
}

func TestRequirePinnedDestinationRejectsUnenforceableProxy(t *testing.T) {
	for _, proxyURL := range []string{"http://proxy.invalid:8080", "https://proxy.invalid:8080", "socks5://proxy.invalid:1080", "socks5h://proxy.invalid:1080"} {
		t.Run(proxyURL, func(t *testing.T) {
			_, err := buildClient(Options{ProxyURL: proxyURL, RequirePinnedDestination: true})
			require.ErrorContains(t, err, "cannot enforce")
			// Trusted fixed-origin proxy clients retain their prior behavior.
			client, err := buildClient(Options{ProxyURL: proxyURL, ValidateResolvedIP: true})
			require.NoError(t, err)
			client.CloseIdleConnections()
		})
	}
	_, err := buildClient(Options{RequirePinnedDestination: true, AllowPrivateHosts: true})
	require.ErrorContains(t, err, "does not allow private hosts")
	require.NotEqual(t, buildClientKey(Options{}), buildClientKey(Options{RequirePinnedDestination: true}))
}

func TestDirectValidatedClientRejectsLoopback(t *testing.T) {
	var reached atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		_, _ = io.WriteString(w, "must not be read")
	}))
	defer server.Close()
	for _, opts := range []Options{{ValidateResolvedIP: true}, {RequirePinnedDestination: true}} {
		client, err := buildClient(opts)
		require.NoError(t, err)
		_, err = client.Get(server.URL)
		require.ErrorContains(t, err, "not allowed")
		client.CloseIdleConnections()
	}
	require.Zero(t, reached.Load())
}

func TestRequirePinnedDestinationDoesNotExposeMalformedProxyCredentials(t *testing.T) {
	for _, proxyURL := range []string{
		"http://user:synthetic-proxy-secret@proxy.invalid:8080",
		"http://user:synthetic-proxy-secret@proxy.invalid:badport",
		"http://user:synthetic-proxy-secret@[invalid",
	} {
		_, err := buildClient(Options{ProxyURL: proxyURL, RequirePinnedDestination: true})
		require.ErrorContains(t, err, "cannot enforce")
		require.NotContains(t, err.Error(), "synthetic-proxy-secret")
	}
}

func TestPublicDestinationIPAllowsOrdinaryPublicIPv4AndIPv6(t *testing.T) {
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "::ffff:8.8.8.8", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		require.True(t, isPublicDestinationIP(net.ParseIP(address)), address)
	}
	require.False(t, isPublicDestinationIP(nil))
}
