package main

import (
	"strings"
	"testing"
)

// envFunc turns a map into the getenv callback LoadConfig takes.
func envFunc(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

// validEnv is a complete set of service URLs, so a test can remove the one it cares
// about and be sure nothing else is missing.
func validEnv() map[string]string {
	return map[string]string{
		"MEMBERS_URL": "http://members:8081",
		"BOOKS_URL":   "http://books:8082",
	}
}

func TestLoadConfigRequiresEveryServiceURL(t *testing.T) {
	for _, name := range ServiceURLVariables {
		t.Run(name, func(t *testing.T) {
			env := validEnv()
			delete(env, name)

			_, err := LoadConfig(envFunc(env))
			if err == nil {
				t.Fatalf("LoadConfig() with %s unset returned no error", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("error %q does not name %s", err, name)
			}
		})
	}
}

func TestLoadConfigRejectsUnparsableURL(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{name: "no scheme", value: "books:8082"},
		{name: "ftp scheme", value: "ftp://books:8082"},
		{name: "no host", value: "http://"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnv()
			env["BOOKS_URL"] = tc.value

			_, err := LoadConfig(envFunc(env))
			if err == nil {
				t.Fatalf("LoadConfig() with BOOKS_URL=%q returned no error", tc.value)
			}
			if !strings.Contains(err.Error(), "BOOKS_URL") {
				t.Fatalf("error %q does not name BOOKS_URL", err)
			}
		})
	}
}

func TestLoadConfigParsesBothServices(t *testing.T) {
	cfg, err := LoadConfig(envFunc(validEnv()))
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	if cfg.MembersURL == nil || cfg.MembersURL.Host != "members:8081" {
		t.Fatalf("MembersURL = %v, want host members:8081", cfg.MembersURL)
	}
	if cfg.BooksURL == nil || cfg.BooksURL.Host != "books:8082" {
		t.Fatalf("BooksURL = %v, want host books:8082", cfg.BooksURL)
	}
	if cfg.UpstreamTimeout != DefaultUpstreamTimeout {
		t.Fatalf("UpstreamTimeout = %v, want %v", cfg.UpstreamTimeout, DefaultUpstreamTimeout)
	}
}
