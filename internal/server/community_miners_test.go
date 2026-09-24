package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCommunityMinerRegistryLoadsValidatedCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"schema_version": 1,
			"miners": [{
				"id": "sample-miner",
				"name": "Sample Miner",
				"version": "1.0.0",
				"hardware": ["NVIDIA"],
				"source_url": "https://github.com/example/miner",
				"package_url": "https://github.com/example/miner/releases/download/v1.0.0/miner.tar.gz",
				"executable_name": "miner",
				"executable_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"default_arguments": ["--pool", "{POOL}"],
				"recommended_algorithm": "samplehash"
			}]
		}`))
	}))
	defer server.Close()

	registry := newCommunityMinerRegistry()
	registry.client = server.Client()
	registry.url = server.URL
	catalog := registry.catalog(context.Background())
	if catalog.Error != "" || len(catalog.Miners) != 1 || catalog.Miners[0].ID != "sample-miner" {
		t.Fatalf("community catalog = %#v", catalog)
	}
}

func TestCommunityMinerRegistryRejectsInvalidDigest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"schema_version": 1,
			"miners": [{
				"id": "unsafe-miner",
				"name": "Unsafe Miner",
				"version": "1.0.0",
				"package_url": "https://example.com/miner",
				"executable_name": "miner",
				"executable_sha256": "not-a-digest",
				"recommended_algorithm": "samplehash"
			}]
		}`))
	}))
	defer server.Close()

	registry := newCommunityMinerRegistry()
	registry.client = server.Client()
	registry.url = server.URL
	catalog := registry.catalog(context.Background())
	if catalog.Error == "" || len(catalog.Miners) != 0 {
		t.Fatalf("invalid catalog was accepted: %#v", catalog)
	}
}
