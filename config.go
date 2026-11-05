package main

import (
	"fmt"
	"net/url"
	"time"
)

// ServiceURLVariables lists the environment variables LoadConfig requires, in the
// order they are validated.
var ServiceURLVariables = []string{"MEMBERS_URL", "BOOKS_URL"}

// DefaultUpstreamTimeout bounds how long the loan service waits on a dependency.
const DefaultUpstreamTimeout = 30 * time.Second

// Config carries the service URLs the loan service calls and the timeout it waits
// on them with.
type Config struct {
	MembersURL      *url.URL
	BooksURL        *url.URL
	UpstreamTimeout time.Duration
}

// LoadConfig reads every service URL through getenv. There is no default: a missing
// or unusable value is an error naming the offending variable.
func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{UpstreamTimeout: DefaultUpstreamTimeout}
	parsed := make(map[string]*url.URL, len(ServiceURLVariables))
	for _, name := range ServiceURLVariables {
		u, err := parseServiceURL(name, getenv(name))
		if err != nil {
			return Config{}, err
		}
		parsed[name] = u
	}
	cfg.MembersURL = parsed["MEMBERS_URL"]
	cfg.BooksURL = parsed["BOOKS_URL"]
	return cfg, nil
}

// parseServiceURL parses one service URL, requiring a non-empty absolute http or
// https URL that names a host.
func parseServiceURL(name, raw string) (*url.URL, error) {
	if raw == "" {
		return nil, fmt.Errorf("%s is required", name)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is not a valid URL: %v", name, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%s must use the http or https scheme, got %q", name, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%s must name a host", name)
	}
	return u, nil
}
