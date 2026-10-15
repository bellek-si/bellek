package bearer_test

import (
	"net/http"
	"testing"

	"github.com/bellek-si/bellek/internal/bearer"
	"github.com/stretchr/testify/require"
)

type recorder []string

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	*r = append(*r, req.URL.Host+" "+req.Header.Get("Authorization"))
	return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
}

func TestTransportSendsTheKeyToItsOwnHostOnly(t *testing.T) {
	var sent recorder
	tr := bearer.New("https://bellek.example/base", "bk_key", &sent)
	for _, u := range []string{"https://bellek.example/v1/me", "https://api.bellek.example/v1/me", "https://bellek.example:8443/v1/me", "https://evil.example/v1/me"} {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		require.NoError(t, err)
		res, err := tr.RoundTrip(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Empty(t, req.Header.Get("Authorization"), "the caller's request is left as it was")
	}
	require.Equal(t, recorder{"bellek.example Bearer bk_key", "api.bellek.example ", "bellek.example:8443 ", "evil.example "}, sent)
}

func TestTransportWithABadBaseURLNeverSendsTheKey(t *testing.T) {
	var sent recorder
	tr := bearer.New("://not a url", "bk_key", &sent)
	req, err := http.NewRequest(http.MethodGet, "https://bellek.example/v1/me", nil)
	require.NoError(t, err)
	res, err := tr.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, recorder{"bellek.example "}, sent)
}
