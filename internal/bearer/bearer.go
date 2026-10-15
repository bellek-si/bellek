package bearer

import (
	"net/http"
	"net/url"
)

// Transport sends the key to the host of the base URL only. A header set on the
// request itself would follow a redirect to a subdomain of that host, or to the
// same host name on another port, because net/http copies it there.
type Transport struct {
	token string
	host  string
	next  http.RoundTripper
}

func New(baseURL, token string, next http.RoundTripper) Transport {
	var host string
	if u, err := url.Parse(baseURL); err == nil {
		host = u.Host
	}
	return Transport{token: token, host: host, next: next}
}

func (t Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != t.host {
		return t.next.RoundTrip(r)
	}
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.next.RoundTrip(clone)
}
