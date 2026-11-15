package platform

import (
	"net"
	"net/http"
	"time"
)

// UserAgent sent to every external service (generic, no personal data).
const UserAgent = "dwts-hackathon/1.0"

type uaTransport struct{ base http.RoundTripper }

func (t uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("User-Agent") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("User-Agent", UserAgent)
	}
	return t.base.RoundTrip(r)
}

// NewHTTPClient returns a shared client with sane timeouts and keep-alive.
func NewHTTPClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{Timeout: timeout, Transport: uaTransport{base: tr}}
}
