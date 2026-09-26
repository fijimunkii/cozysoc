package adguard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

func TestReadOnlySnapshot(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		user, pass, ok := r.BasicAuth()
		if !ok || user != "reader" || pass != "secret" {
			t.Error("missing credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.RequestURI() {
		case "/control/status":
			fmt.Fprint(w, `{"version":"v0.107.79","running":true,"protection_enabled":true}`)
		case "/control/filtering/status":
			fmt.Fprint(w, `{"enabled":true,"user_rules":["private-rule"]}`)
		case "/control/querylog/config":
			fmt.Fprint(w, `{"enabled":true,"anonymize_client_ip":true}`)
		case "/control/querylog?limit=100":
			fmt.Fprint(w, `{"data":[{"time":"2026-09-26T12:00:00Z","client":"192.0.2.3","question":{"name":"example.test","type":"A"},"status":"NOERROR","reason":"FilteredBlackList"},{"time":"2026-09-26T12:00:01Z","client_id":"device-1","question":{"name":"example.org","type":"AAAA"},"status":"NOERROR","reason":"NotFilteredNotFound"}]}`)
		default:
			t.Errorf("unexpected path %q", r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "reader", secretstore.NewSecret([]byte("secret")))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 || strings.Join(paths, ",") != "GET /control/status,GET /control/filtering/status,GET /control/querylog/config,GET /control/querylog?limit=100" {
		t.Fatalf("unexpected requests: %v", paths)
	}
	if !snapshot.Status.Running || !snapshot.Status.ProtectionEnabled || !snapshot.Status.FilteringEnabled || !snapshot.Status.QueryLogEnabled || !snapshot.Status.AnonymizedClients {
		t.Fatalf("wrong status: %+v", snapshot.Status)
	}
	if len(snapshot.Queries) != 2 || snapshot.Queries[0].Filtering != "blocked" || snapshot.Queries[0].Attribution != "unknown" || snapshot.Queries[0].ClientIP != "" || snapshot.Queries[1].Attribution != "client-id" || snapshot.Queries[1].Filtering != "not-blocked" {
		t.Fatalf("wrong observations: %+v", snapshot.Queries)
	}
}

func TestDisabledQueryLogDoesNotFetchHistory(t *testing.T) {
	var queryFetches int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/control/status":
			fmt.Fprint(w, `{"version":"v0.107.79","running":true,"protection_enabled":false}`)
		case "/control/filtering/status":
			fmt.Fprint(w, `{"enabled":false}`)
		case "/control/querylog/config":
			fmt.Fprint(w, `{"enabled":false,"anonymize_client_ip":false}`)
		case "/control/querylog":
			queryFetches++
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "", secretstore.Secret{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Read(context.Background())
	if err != nil || queryFetches != 0 || len(snapshot.Queries) != 0 || snapshot.Status.QueryLogEnabled {
		t.Fatalf("unexpected disabled result: %+v, %v, %d fetches", snapshot, err, queryFetches)
	}
}

func TestEndpointAndResponseBoundaries(t *testing.T) {
	for _, endpoint := range []string{"http://example.test", "http://localhost:3000", "https://example.test", "file:///tmp/test", "https://user:pass@127.0.0.1", "https://127.0.0.1/path", "https://127.0.0.1/%2F", "https://127.0.0.1?", "https://127.0.0.1/?q=1", "https://127.0.0.1/#part", "https://127.0.0.1:0"} {
		if _, err := NewClient(endpoint, "", secretstore.Secret{}); !errors.Is(err, ErrEndpoint) {
			t.Errorf("accepted endpoint %q: %v", endpoint, err)
		}
	}
	for _, tc := range []struct {
		name, body  string
		code        int
		contentType string
		want        error
	}{
		{"auth", "private upstream error", http.StatusUnauthorized, "text/plain", ErrAuth},
		{"redirect", "", http.StatusFound, "text/plain", ErrUnavailable},
		{"wrong version", `{"version":"v0.108.0","running":true,"protection_enabled":true}`, http.StatusOK, "application/json", ErrVersion},
		{"missing field", `{"version":"v0.107.79","running":true}`, http.StatusOK, "application/json", ErrResponse},
		{"oversize", strings.Repeat("x", maxResponseBytes+1), http.StatusOK, "application/json", ErrResponse},
		{"html", "<html>login</html>", http.StatusOK, "text/html", ErrResponse},
		{"trailing json", `{} {}`, http.StatusOK, "application/json", ErrResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				if tc.code == http.StatusFound {
					w.Header().Set("Location", "http://127.0.0.1:1/steal")
				}
				w.WriteHeader(tc.code)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "", secretstore.Secret{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Read(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if strings.Contains(fmt.Sprint(err), "private") || strings.Contains(fmt.Sprint(err), server.URL) {
				t.Fatalf("untrusted content leaked: %v", err)
			}
		})
	}
}

func TestRedirectNeverForwardsCredentials(t *testing.T) {
	var targetRequests int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests++
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/private", http.StatusFound)
	}))
	defer source.Close()
	client, err := NewClient(source.URL, "reader", secretstore.NewSecret([]byte("secret")))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Read(context.Background())
	if !errors.Is(err, ErrUnavailable) || targetRequests != 0 {
		t.Fatalf("redirect escaped origin: %v, %d requests", err, targetRequests)
	}
}

func TestMalformedQueryFailsClosed(t *testing.T) {
	for _, query := range []string{
		`{"time":"2026-09-26T12:00:00Z","client":"192.0.2.3","question":{"name":"<script>.test","type":"A"},"status":"NOERROR","reason":"FilteredBlackList"}`,
		`{"time":"2026-09-26T12:00:00Z","client":"not-an-ip","question":{"name":"example.test","type":"A"},"status":"NOERROR","reason":"FilteredBlackList"}`,
		`{"time":"2026-09-26T12:00:00Z","client":"192.0.2.3","question":{"name":"example.test","type":"A"},"status":"NOERROR","reason":"NewUpstreamReason"}`,
	} {
		t.Run(query, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/control/status":
					fmt.Fprint(w, `{"version":"v0.107.79","running":true,"protection_enabled":true}`)
				case "/control/filtering/status":
					fmt.Fprint(w, `{"enabled":true}`)
				case "/control/querylog/config":
					fmt.Fprint(w, `{"enabled":true,"anonymize_client_ip":false}`)
				case "/control/querylog":
					fmt.Fprintf(w, `{"data":[%s]}`, query)
				}
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "", secretstore.Secret{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Read(context.Background()); !errors.Is(err, ErrResponse) {
				t.Fatalf("accepted untrusted query: %v", err)
			}
		})
	}
}

func TestMissingQueryArrayIsNotReportedAsQuiet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/control/status":
			fmt.Fprint(w, `{"version":"v0.107.79","running":true,"protection_enabled":true}`)
		case "/control/filtering/status":
			fmt.Fprint(w, `{"enabled":true}`)
		case "/control/querylog/config":
			fmt.Fprint(w, `{"enabled":true,"anonymize_client_ip":false}`)
		case "/control/querylog":
			fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "", secretstore.Secret{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(context.Background()); !errors.Is(err, ErrResponse) {
		t.Fatalf("missing query array became an empty history: %v", err)
	}
}
