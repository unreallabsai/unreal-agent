package responsesapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/unreallabsai/unreal-agent/harness/llm"
	"github.com/unreallabsai/unreal-agent/harness/primitives"
)

type retryTestReply struct {
	status  int
	body    string
	headers http.Header
}

type retryTestTransport struct {
	replies []retryTestReply
	calls   []time.Time
}

func (transport *retryTestTransport) RoundTrip(*http.Request) (*http.Response, error) {
	reply := transport.replies[min(len(transport.calls), len(transport.replies)-1)]
	transport.calls = append(transport.calls, time.Now())
	headers := reply.headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	if reply.status == http.StatusOK {
		headers.Set("Content-Type", "text/event-stream")
	} else {
		headers.Set("Content-Type", "application/json")
	}
	return &http.Response{StatusCode: reply.status, Header: headers, Body: io.NopCloser(strings.NewReader(reply.body))}, nil
}

func TestExchangeRetriesShareAttemptLimit(t *testing.T) {
	for _, test := range []struct {
		name    string
		replies []retryTestReply
		limit   int
		success bool
	}{
		{name: "top-level exhausted", limit: 2, replies: []retryTestReply{{200, retryErrorEvent("error", "server_error", "retry"), nil}}},
		{name: "nested exhausted", limit: 2, replies: []retryTestReply{{200, retryErrorEvent("response.failed", "server_error", "retry"), nil}}},
		{name: "disabled", limit: 1, replies: []retryTestReply{{200, retryErrorEvent("response.failed", "rate_limit_exceeded", "try again in 120s"), nil}}},
		{name: "HTTP then nested", limit: 2, replies: []retryTestReply{{503, "", nil}, {200, retryErrorEvent("response.failed", "server_error", "retry"), nil}}},
		{name: "nested then HTTP", limit: 2, replies: []retryTestReply{{200, retryErrorEvent("response.failed", "server_error", "retry"), nil}, {503, "", nil}}},
		{name: "mixed recovery", limit: 4, success: true, replies: []retryTestReply{
			{503, "", nil},
			{200, retryErrorEvent("response.failed", "server_error", "retry"), nil},
			{200, retryErrorEvent("error", "rate_limit_exceeded", "try again in 1ms"), nil},
			{200, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"ok\",\"status\":\"completed\",\"output\":[]}}\n\n", nil},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				transport := &retryTestTransport{replies: test.replies}
				remote := primitives.NewRemoteClientWithHTTPClient(&http.Client{Transport: transport})
				defer remote.Close()
				adapter, err := NewAdapter(remote, Config{Endpoint: "http://example.invalid/responses", MaxAttempts: &test.limit})
				if err != nil {
					t.Fatal(err)
				}
				response, err := adapter.Respond(t.Context(), validRequest(), llm.RequestOptions{})
				if len(transport.calls) != test.limit || (err == nil && response.Failure == nil) != test.success {
					t.Fatalf("calls=%d response=%#v error=%v", len(transport.calls), response, err)
				}
				if !time.Now().Equal(transport.calls[len(transport.calls)-1]) {
					t.Fatal("waited after the final attempt")
				}
				if test.success && response.ID != "ok" {
					t.Fatalf("response=%#v", response)
				}
			})
		})
	}
}

func TestExchangeRetryHints(t *testing.T) {
	for _, test := range []struct {
		name, kind, code, message, header string
		status                            int
		want                              time.Duration
	}{
		{name: "top-level", kind: "error", code: "rate_limit_exceeded", message: "try again in 1.25s", status: 200, want: 1250 * time.Millisecond},
		{name: "nested", kind: "response.failed", code: "rate_limit_exceeded", message: "try again in 28ms", status: 200, want: 28 * time.Millisecond},
		{name: "HTTP body", kind: "http", code: "rate_limit_exceeded", message: "try again in 45s", status: 429, want: 30 * time.Second},
		{name: "HTTP header", kind: "http", status: 503, header: "120", want: 30 * time.Second},
		{name: "overload header", kind: "http", code: "server_is_overloaded", message: "overloaded", status: 503, header: "90", want: 30 * time.Second},
		{name: "nested header", kind: "response.failed", code: "rate_limit_exceeded", message: "try again in 1s", status: 200, header: "90", want: 30 * time.Second},
		{name: "HTTP date", kind: "http", status: 503, header: "date", want: 30 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				header := test.header
				if header == "date" {
					header = time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)
				}
				body := retryErrorEvent(test.kind, test.code, test.message)
				if test.kind == "http" {
					body = fmt.Sprintf(`{"error":{"code":%q,"message":%q}}`, test.code, test.message)
				}
				transport := &retryTestTransport{replies: []retryTestReply{{test.status, body, http.Header{"Retry-After": {header}}}}}
				remote := primitives.NewRemoteClientWithHTTPClient(&http.Client{Transport: transport})
				defer remote.Close()
				adapter, err := NewAdapter(remote, Config{Endpoint: "http://example.invalid/responses", MaxAttempts: new(2)})
				if err != nil {
					t.Fatal(err)
				}
				response, err := adapter.Respond(t.Context(), validRequest(), llm.RequestOptions{})
				if len(transport.calls) != 2 || transport.calls[1].Sub(transport.calls[0]) != test.want {
					t.Fatalf("request times=%v response=%#v error=%v", transport.calls, response, err)
				}
				if test.code != "" {
					assertResponseError(t, test.kind, test.code, response, err)
				}
			})
		})
	}
}

func TestExchangeBackoffUsesTimer(t *testing.T) {
	for _, test := range []struct {
		name, kind, code string
		status           int
		base             time.Duration
	}{
		{name: "HTTP", kind: "http", status: 503, base: 2 * time.Second},
		{name: "top-level", kind: "error", status: 200, code: "server_error", base: 2 * time.Second},
		{name: "nested", kind: "response.failed", status: 200, code: "server_error", base: 2 * time.Second},
		{name: "HTTP overload", kind: "http", status: 503, code: "server_is_overloaded", base: 10 * time.Second},
		{name: "nested overload", kind: "response.failed", status: 200, code: "slow_down", base: 10 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				body := retryErrorEvent(test.kind, test.code, "retry")
				if test.kind == "http" {
					body = fmt.Sprintf(`{"error":{"code":%q}}`, test.code)
				}
				transport := &retryTestTransport{replies: []retryTestReply{{test.status, body, nil}}}
				remote := primitives.NewRemoteClientWithHTTPClient(&http.Client{Transport: transport})
				defer remote.Close()
				adapter, err := NewAdapter(remote, Config{Endpoint: "http://example.invalid/responses", MaxAttempts: new(3)})
				if err != nil {
					t.Fatal(err)
				}
				response, err := adapter.Respond(t.Context(), validRequest(), llm.RequestOptions{})
				if len(transport.calls) != 3 || err == nil && response.Failure == nil {
					t.Fatalf("request times=%v error=%v", transport.calls, err)
				}
				for i := 1; i < len(transport.calls); i++ {
					base := test.base * time.Duration(1<<(i-1))
					delay := transport.calls[i].Sub(transport.calls[i-1])
					if delay < base*4/5 || delay > base {
						t.Fatalf("backoff=%s, expected [%s,%s]", delay, base*4/5, base)
					}
				}
			})
		})
	}
}

func TestExchangeBackoffCancellation(t *testing.T) {
	for _, reply := range []retryTestReply{
		{503, "", nil},
		{503, "", http.Header{"Retry-After": {"3600"}}},
		{200, retryErrorEvent("response.failed", "rate_limit_exceeded", "try again in 3600s"), nil},
	} {
		synctest.Test(t, func(t *testing.T) {
			transport := &retryTestTransport{replies: []retryTestReply{reply}}
			remote := primitives.NewRemoteClientWithHTTPClient(&http.Client{Transport: transport})
			defer remote.Close()
			adapter, err := NewAdapter(remote, Config{Endpoint: "http://example.invalid/responses", MaxAttempts: new(3)})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := adapter.Respond(ctx, validRequest(), llm.RequestOptions{}); done <- err }()
			synctest.Wait()
			if len(transport.calls) != 1 {
				t.Fatalf("requests=%d", len(transport.calls))
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel error=%v", err)
			}
			ctx, stop := context.WithTimeout(t.Context(), time.Second)
			defer stop()
			if _, err := adapter.Respond(ctx, validRequest(), llm.RequestOptions{}); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline error=%v", err)
			}
			if len(transport.calls) != 2 {
				t.Fatal("retried after cancellation or deadline")
			}
		})
	}
}
