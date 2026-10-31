package server

import (
	"strings"
	"testing"
)

func TestPrivacyPage(t *testing.T) {
	e := setup(t)
	resp := e.do("GET", "/privacy", nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("privacy page: %d %v", resp.StatusCode, resp.Header)
	}
}
