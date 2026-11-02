package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// services talks to the member and catalog services. Nothing here reads their
// databases; the only shared truth is the HTTP contract.
type services struct {
	memberURL  string
	catalogURL string
	client     *http.Client
}

func newServices(memberURL, catalogURL string) *services {
	return &services{
		memberURL:  memberURL,
		catalogURL: catalogURL,
		client:     &http.Client{Timeout: 5 * time.Second},
	}
}

// memberExists reports whether the member service knows this id. A transport
// failure is returned as an error, never silently read as "missing".
func (s *services) memberExists(ctx context.Context, id int64) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/members/%d", s.memberURL, id), nil)
	if err != nil {
		return false, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("member service answered %d", resp.StatusCode)
	}
}

// adjust asks the catalog to take one copy off the shelf (-1) or put it back (+1).
func (s *services) adjust(ctx context.Context, bookID int64, delta int) (int, error) {
	action := "reserve"
	if delta > 0 {
		action = "release"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/books/%d/%s", s.catalogURL, bookID, action), nil)
	if err != nil {
		return 0, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}
