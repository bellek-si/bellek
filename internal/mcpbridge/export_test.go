package mcpbridge

import (
	"encoding/json"
	"net/http"
)

func Fill(schema any, raw json.RawMessage, locals Locals) (map[string]any, error) {
	return fill(schema, raw, locals, LocalsFrom)
}

func HTTPClient(baseURL, token string) *http.Client {
	return transport(baseURL, token).HTTPClient
}
