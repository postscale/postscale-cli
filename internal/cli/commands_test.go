package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fixtureID = "11111111-1111-4111-8111-111111111111"
const messageJSON = `{"from":"sender@example.com","to":["recipient@example.net"],"subject":"CLI test","text_body":"Hello"}`

func apiHarness(t *testing.T, handler http.HandlerFunc) *harness {
	t.Helper()
	h := newHarness(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ps_test_fixture" {
			t.Errorf("auth header: %q", got)
		}
		if got := r.UserAgent(); !strings.HasPrefix(got, "postscale-cli/test postscale-go/") {
			t.Errorf("user agent: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "fixture-request")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	h.env["POSTSCALE_API_KEY"] = "ps_test_fixture"
	h.env["POSTSCALE_BASE_URL"] = server.URL
	return h
}

func decodeOutput(t *testing.T, h *harness) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &value); err != nil {
		t.Fatalf("invalid output %s: %v (stderr %s)", &h.stdout, err, &h.stderr)
	}
	return value
}

func TestSendFileAndStdin(t *testing.T) {
	for _, stdin := range []bool{true, false} {
		t.Run(strconv.FormatBool(stdin), func(t *testing.T) {
			var calls atomic.Int32
			h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.URL.Path != "/v1/send" {
					t.Errorf("request: %s %s", r.Method, r.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["text_body"] != "Hello" || body["from"] != "sender@example.com" {
					t.Errorf("body: %v", body)
				}
				io.WriteString(w, `{"message_id":"smtp-id@example.com","status":"simulated","environment":"test"}`)
			})
			path := "-"
			if !stdin {
				path = filepath.Join(t.TempDir(), "message.json")
				if err := os.WriteFile(path, []byte(messageJSON), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if code := h.run(messageJSON, "emails", "send", "--file", path, "--json"); code != 0 {
				t.Fatalf("send: %d %s", code, &h.stderr)
			}
			value := decodeOutput(t, h)
			if calls.Load() != 1 || value["data"].(map[string]any)["status"] != "simulated" {
				t.Fatalf("send output: %v calls %d", value, calls.Load())
			}
			if value["context"].(map[string]any)["credential_environment"] != "test" || value["request_id"] != "fixture-request" {
				t.Fatalf("diagnostics: %v", value)
			}
		})
	}
}

func TestSendNeverRetries(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var calls atomic.Int32
			h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				io.WriteString(w, `{"error":"fixture failure"}`)
			})
			if code := h.run(messageJSON, "emails", "send", "--file", "-", "--retries", "5"); code == 0 {
				t.Fatal("expected failure")
			}
			if calls.Load() != 1 || h.stdout.Len() != 0 {
				t.Fatalf("mutation retries/output: %d %s", calls.Load(), &h.stdout)
			}
		})
	}
	var calls atomic.Int32
	h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close() // Server accepted the request but no response reached the client.
	})
	if code := h.run(messageJSON, "emails", "send", "--file", "-", "--retries", "5"); code != 5 {
		t.Fatalf("network exit: %d %s", code, &h.stderr)
	}
	if calls.Load() != 1 || !strings.Contains(h.stderr.String(), "before resubmitting") {
		t.Fatalf("unsafe retry behavior: %d %s", calls.Load(), &h.stderr)
	}
}

func TestSendValidationAndDryRunNeverContactAPI(t *testing.T) {
	var calls atomic.Int32
	h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) })
	for _, input := range []string{"null", "[]", "{", `{}`, messageJSON + ` {}`, strings.Replace(messageJSON, "text_body", "html", 1), strings.Replace(messageJSON, `"recipient@example.net"`, `""`, 1), strings.Replace(messageJSON, `"Hello"`, `""`, 1)} {
		if code := h.run(input, "emails", "send", "--file", "-"); code != 2 {
			t.Fatalf("invalid input %q: %d %s", input, code, &h.stderr)
		}
	}
	delete(h.env, "POSTSCALE_API_KEY")
	if code := h.run(messageJSON, "emails", "send", "--file", "-", "--dry-run"); code != 0 {
		t.Fatalf("dry run: %d %s", code, &h.stderr)
	}
	if decodeOutput(t, h)["data"].(map[string]any)["submitted"] != false || calls.Load() != 0 {
		t.Fatal("local validation contacted the API")
	}
}

func TestPaginationPreservesFiltersAndUsesActualPageSize(t *testing.T) {
	var offsets []int
	h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/emails" || r.URL.Query().Get("subject") != "receipt" || r.URL.Query().Get("limit") != "100" {
			t.Errorf("query: %s", r.URL)
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		offsets = append(offsets, offset)
		// A server may cap pages below the requested size. The total still
		// indicates more results, and offsets must use the returned count.
		if offset == 0 {
			io.WriteString(w, `{"emails":[{"id":"a"},{"id":"b"}],"total":3}`)
		} else {
			io.WriteString(w, `{"emails":[{"id":"c"}],"total":3}`)
		}
	})
	if code := h.run("", "emails", "list", "--subject", "receipt", "--limit", "100", "--all"); code != 0 {
		t.Fatalf("list: %d %s", code, &h.stderr)
	}
	value := decodeOutput(t, h)
	if fmt.Sprint(offsets) != "[0 2]" || len(value["data"].([]any)) != 3 || value["pagination"].(map[string]any)["has_more"] != false {
		t.Fatalf("pagination: %v %v", offsets, value)
	}
}

func TestDomainListWithoutTotalAndNameResolution(t *testing.T) {
	var verified atomic.Int32
	h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/domains":
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			if r.URL.Query().Get("limit") == "1" && offset > 0 {
				io.WriteString(w, `{"domains":[],"limit":1,"offset":1}`)
				return
			}
			fmt.Fprintf(w, `{"domains":[{"id":%q,"domain":"Example.COM"}],"limit":100,"offset":0}`, fixtureID)
		case "/v1/domains/" + fixtureID + "/verify":
			verified.Add(1)
			if r.Method != "POST" {
				t.Error("verification must use POST")
			}
			io.WriteString(w, `{"verified":false,"spf_verified":false,"summary":"SPF record missing"}`)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(404)
		}
	})
	if code := h.run("", "domains", "list", "--all", "--limit", "1"); code != 0 {
		t.Fatalf("domain list: %d %s", code, &h.stderr)
	}
	if len(decodeOutput(t, h)["data"].([]any)) != 1 {
		t.Fatal("lost domain")
	}
	if code := h.run("", "domains", "verify", "example.com."); code != 1 {
		t.Fatalf("verification exit: %d %s", code, &h.stderr)
	}
	if verified.Load() != 1 || decodeOutput(t, h)["data"].(map[string]any)["verified"] != false || !strings.Contains(h.stderr.String(), "domain_not_verified") {
		t.Fatal("missing failure diagnostics")
	}
}

func TestPaginationRejectsRepeatedAndInconsistentPages(t *testing.T) {
	for _, fixture := range []string{`{"emails":[{"id":"same"}],"total":10}`, `{"emails":[],"total":10}`, `{"emails":[{"id":"a"},{"id":"b"}],"total":1}`} {
		var calls atomic.Int32
		h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, fixture) })
		if code := h.run("", "emails", "list", "--all", "--limit", "1"); code != 1 {
			t.Fatalf("expected protocol error: %d %s", code, &h.stderr)
		}
		if calls.Load() > 2 || h.stdout.Len() != 0 || !strings.Contains(h.stderr.String(), "invalid_pagination") {
			t.Fatalf("invalid pagination handling: %d %s", calls.Load(), &h.stderr)
		}
	}
}

func TestResourceRoutesAndEnvelopes(t *testing.T) {
	cases := []struct {
		args                   []string
		method, path, response string
	}{
		{[]string{"domains", "get", fixtureID}, "GET", "/v1/domains/" + fixtureID, `{"id":"` + fixtureID + `","domain":"example.com"}`},
		{[]string{"domains", "dns", fixtureID}, "GET", "/v1/domains/" + fixtureID + "/dns", `{"domain":"example.com","records":[]}`},
		{[]string{"domains", "verify", fixtureID}, "POST", "/v1/domains/" + fixtureID + "/verify", `{"verified":true}`},
		{[]string{"domains", "create", "example.com"}, "POST", "/v1/domains", `{"id":"` + fixtureID + `","domain":"example.com"}`},
		{[]string{"emails", "get", fixtureID}, "GET", "/v1/emails/" + fixtureID, `{"email":{"id":"` + fixtureID + `","status":"delivered"}}`},
		{[]string{"emails", "events", fixtureID}, "GET", "/v1/emails/" + fixtureID + "/events", `{"events":[{"event_type":"delivered"}]}`},
		{[]string{"inbound", "get", fixtureID}, "GET", "/v1/inbound-emails/" + fixtureID, `{"email":{"id":"` + fixtureID + `","subject":"Inbound"}}`},
		{[]string{"inbound", "list", "--query", "receipt"}, "GET", "/v1/inbound-emails", `{"emails":[],"total":0,"limit":50,"offset":0}`},
		{[]string{"webhooks", "list"}, "GET", "/v1/webhooks", `{"webhooks":[{"id":"` + fixtureID + `","url":"https://example.com/hook"}]}`},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args[:2], "/"), func(t *testing.T) {
			var calls atomic.Int32
			h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("%s %s", r.Method, r.URL)
				}
				if tc.path == "/v1/webhooks" && r.URL.RawQuery != "" {
					t.Error("unpaginated webhook request includes pagination")
				}
				if tc.args[0] == "inbound" && tc.args[1] == "list" && r.URL.Query().Get("q") != "receipt" {
					t.Error("query dropped")
				}
				if tc.args[0] == "domains" && tc.args[1] == "create" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["type"] != "outbound" {
						t.Errorf("create body: %v %v", body, err)
					}
				}
				io.WriteString(w, tc.response)
			})
			if code := h.run("", tc.args...); code != 0 {
				t.Fatalf("exit %d: %s", code, &h.stderr)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls: %d", calls.Load())
			}
			if decodeOutput(t, h)["request_id"] != "fixture-request" {
				t.Fatal("request ID omitted")
			}
		})
	}
}

func TestWebhookPartialHistory(t *testing.T) {
	h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/webhook-deliveries" || r.URL.Query().Get("status") != "failed" || r.URL.Query().Get("endpoint_id") != fixtureID {
			t.Errorf("request: %s", r.URL)
		}
		io.WriteString(w, `{"deliveries":[],"total":0,"warnings":[{"source":"deliveries","message":"temporarily unavailable"}]}`)
	})
	if code := h.run("", "webhooks", "deliveries", "list", "--status", "failed", "--endpoint-id", fixtureID, "--all"); code != 1 {
		t.Fatalf("partial result exit: %d %s", code, &h.stderr)
	}
	if len(decodeOutput(t, h)["warnings"].([]any)) != 1 || !strings.Contains(h.stderr.String(), "partial_result") {
		t.Fatal("missing partial-result diagnostics")
	}
}

func TestAPIErrorsAndReadRetry(t *testing.T) {
	for status, wantExit := range map[int]int{401: 3, 403: 3, 429: 4, 500: 1} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(status)
				io.WriteString(w, `{"error":{"code":"fixture_error","message":"rejected ps_test_fixture"}}`)
			})
			if code := h.run("", "emails", "list", "--retries", "0", "--json"); code != wantExit {
				t.Fatalf("exit %d: %s", code, &h.stderr)
			}
			if h.stdout.Len() != 0 || strings.Contains(h.stderr.String(), "ps_test_fixture") || !strings.Contains(h.stderr.String(), "fixture-request") || !strings.Contains(h.stderr.String(), `"retry_after_seconds":2`) {
				t.Fatalf("error contract: %s", &h.stderr)
			}
		})
	}
	var calls atomic.Int32
	h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
			io.WriteString(w, `{"error":"temporary"}`)
			return
		}
		io.WriteString(w, `{"emails":[],"total":0}`)
	})
	if code := h.run("", "emails", "list"); code != 0 || calls.Load() != 2 {
		t.Fatalf("read retry: %d %d %s", code, calls.Load(), &h.stderr)
	}
}

func TestTimeoutCancellationAndRedirectRejection(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		if code := h.run("", "emails", "list", "--timeout", "20ms"); code != 5 {
			t.Fatalf("timeout exit %d: %s", code, &h.stderr)
		}
	})
	t.Run("retry delay is bounded", func(t *testing.T) {
		h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(429)
		})
		start := time.Now()
		if code := h.run("", "emails", "list", "--timeout", "20ms"); code != 5 || time.Since(start) > time.Second {
			t.Fatalf("unbounded retry: %d %s", code, &h.stderr)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) { t.Error("canceled command made request") })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		h.app.in = strings.NewReader("")
		if code := h.app.execute(ctx, []string{"emails", "list"}); code != 130 {
			t.Fatalf("cancellation exit %d: %s", code, &h.stderr)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		var targetCalls atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
		defer target.Close()
		h := apiHarness(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
		})
		if code := h.run(messageJSON, "emails", "send", "--file", "-"); code != 1 || targetCalls.Load() != 0 {
			t.Fatalf("redirect followed: %d %d", code, targetCalls.Load())
		}
	})
}
