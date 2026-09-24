package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestApplicationVersionLess(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{current: "0.2.0-beta.1", latest: "0.2.0-beta.2", want: true},
		{current: "0.2.0-beta.2", latest: "0.2.0", want: true},
		{current: "0.2.0", latest: "0.2.0-beta.3", want: false},
		{current: "0.2.0", latest: "0.3.0", want: true},
		{current: "1.0.0", latest: "1.0.0", want: false},
	}
	for _, test := range tests {
		if got := applicationVersionLess(test.current, test.latest); got != test.want {
			t.Fatalf("applicationVersionLess(%q, %q) = %v, want %v", test.current, test.latest, got, test.want)
		}
	}
}

func TestApplicationUpdateMonitorUsesLatestPublishedRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"tag_name":"v0.2.0-beta.2","html_url":"https://example.test/release","draft":false,"prerelease":true},
			{"tag_name":"v9.0.0","draft":true}
		]`))
	}))
	defer server.Close()
	monitor := newApplicationUpdateMonitor()
	monitor.client = server.Client()
	monitor.url = server.URL
	release, err := monitor.latest(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if release.TagName != "v0.2.0-beta.2" {
		t.Fatalf("latest release = %#v", release)
	}
}

func TestReadReleaseManifestRejectsUnsafeEntry(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "SHA256SUMS.json")
	if err := os.WriteFile(filename, []byte(`[{"Hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","File":"../server.exe"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readReleaseManifest(filename); err == nil {
		t.Fatal("unsafe release manifest entry was accepted")
	}
}
