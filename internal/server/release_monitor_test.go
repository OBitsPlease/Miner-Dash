package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCatalogReleaseMonitorCachesLatestRelease(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"3.6.9","html_url":"https://github.com/doktor83/SRBMiner-Multi/releases/tag/3.6.9"}`))
	}))
	defer server.Close()
	monitor := newCatalogReleaseMonitor()
	monitor.client = server.Client()
	monitor.apiBase = server.URL + "/"
	source := catalogReleaseSource{Repository: "doktor83/SRBMiner-Multi"}

	first := monitor.latest(context.Background(), source)
	second := monitor.latest(context.Background(), source)
	if first.Version != "3.6.9" || second.URL == "" || requests != 1 {
		t.Fatalf("release results = %#v, %#v; requests = %d", first, second, requests)
	}
	if time.Since(first.CheckedAt) > time.Minute {
		t.Fatalf("unexpected check time: %s", first.CheckedAt)
	}
}

func TestVersionLess(t *testing.T) {
	tests := []struct {
		installed string
		latest    string
		want      bool
	}{
		{installed: "3.6.7", latest: "3.6.9", want: true},
		{installed: "v0.51.2", latest: "0.51.2", want: false},
		{installed: "2.5e3", latest: "2.5e2", want: false},
		{installed: "package-required", latest: "1.0", want: false},
	}
	for _, test := range tests {
		if got := versionLess(test.installed, test.latest); got != test.want {
			t.Fatalf("versionLess(%q, %q) = %v, want %v", test.installed, test.latest, got, test.want)
		}
	}
}
