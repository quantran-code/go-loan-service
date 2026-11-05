package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The clients map the status a dependency answered with onto a sentinel error, so
// the handlers never have to know those status codes.
var (
	// errMemberNotFound means the members service answered 404 for the member.
	errMemberNotFound = errors.New("member not found")
	// errBookNotFound means the books service does not know the book.
	errBookNotFound = errors.New("book not found")
	// errBookOnLoan means the books service already has the book out.
	errBookOnLoan = errors.New("book already on loan")
	// errUpstreamUnavailable covers transport failures, timeouts and every status
	// the clients do not otherwise map.
	errUpstreamUnavailable = errors.New("upstream unavailable")
)

// membersClient asks the members service whether a member exists.
type membersClient struct {
	base *url.URL
	http *http.Client
}

// newMembersClient builds a members client rooted at base.
func newMembersClient(base *url.URL, timeout time.Duration) *membersClient {
	return &membersClient{base: base, http: &http.Client{Timeout: timeout}}
}

// memberExists performs GET <base>/members/<id>. A 200 means the member exists, a
// 404 becomes errMemberNotFound; any other answer or a transport failure becomes
// errUpstreamUnavailable.
func (m *membersClient) memberExists(ctx context.Context, id int64) error {
	if m == nil || m.base == nil || m.http == nil {
		return errUpstreamUnavailable
	}
	status, err := sendUpstream(ctx, m.http, http.MethodGet, m.base.JoinPath("members", strconv.FormatInt(id, 10)))
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return errMemberNotFound
	default:
		return errUpstreamUnavailable
	}
}

// booksClient asks the books service to take a book out on loan.
type booksClient struct {
	base *url.URL
	http *http.Client
}

// newBooksClient builds a books client rooted at base.
func newBooksClient(base *url.URL, timeout time.Duration) *booksClient {
	return &booksClient{base: base, http: &http.Client{Timeout: timeout}}
}

// borrow performs POST <base>/books/<id>/borrow. The books service owns the
// availability check, so a 200 means the book was marked on loan, a 404 becomes
// errBookNotFound and a 409 becomes errBookOnLoan; any other answer or a transport
// failure becomes errUpstreamUnavailable.
func (b *booksClient) borrow(ctx context.Context, bookID int64) error {
	if b == nil || b.base == nil || b.http == nil {
		return errUpstreamUnavailable
	}
	status, err := sendUpstream(ctx, b.http, http.MethodPost, b.base.JoinPath("books", strconv.FormatInt(bookID, 10), "borrow"))
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return errBookNotFound
	case http.StatusConflict:
		return errBookOnLoan
	default:
		return errUpstreamUnavailable
	}
}

// sendUpstream performs one request against a dependency, forwarding the inbound
// request ID from ctx so the correlation survives the hop, and reports the status
// code. Transport failures and timeouts are reported as errUpstreamUnavailable.
func sendUpstream(ctx context.Context, client *http.Client, method string, target *url.URL) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return 0, errUpstreamUnavailable
	}
	if id := RequestIDFromContext(ctx); id != "" {
		req.Header.Set(RequestIDHeader, id)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, errUpstreamUnavailable
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
