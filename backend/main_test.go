package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeLoginAndClusterRequestsUseOnlySessionCredential(t *testing.T) {
	var receivedAuthorisation string
	es := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthorisation = r.Header.Get("Authorization")
		username, password, ok := r.BasicAuth()
		if !ok || username != "alice" || password != "secret" {
			http.Error(w, `{"error":"unauthorised"}`, http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/_security/_authenticate":
			_, _ = io.WriteString(w, `{"username":"alice"}`)
		case "/":
			_, _ = io.WriteString(w, `{"cluster_name":"test-cluster","version":{"number":"8.17.0"}}`)
		case "/_cluster/health":
			_, _ = io.WriteString(w, `{"status":"green","number_of_nodes":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer es.Close()

	s := newTestServer(es.URL, "development")
	login := request(http.MethodPost, "/api/clusters/test/login", `{"username":"alice","password":"secret"}`)
	loginRecorder := httptest.NewRecorder()
	s.clusterHandler(loginRecorder, login)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRecorder.Code, loginRecorder.Body.String())
	}
	if strings.Contains(loginRecorder.Body.String(), "secret") {
		t.Fatal("login response leaked the password")
	}
	cookie := loginRecorder.Result().Cookies()[0]

	overview := request(http.MethodGet, "/api/clusters/test/overview", "")
	overview.AddCookie(cookie)
	overviewRecorder := httptest.NewRecorder()
	s.clusterHandler(overviewRecorder, overview)
	if overviewRecorder.Code != http.StatusOK {
		t.Fatalf("overview status = %d, body = %s", overviewRecorder.Code, overviewRecorder.Body.String())
	}
	if receivedAuthorisation == "" {
		t.Fatal("Elasticsearch request did not receive a basic auth header")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(overviewRecorder.Body.Bytes(), &body); err != nil || len(body["health"]) == 0 {
		t.Fatalf("overview response = %q", overviewRecorder.Body.String())
	}
}

func TestProductionMutationRequiresConfirmationBeforeProxying(t *testing.T) {
	called := false
	es := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path == "/_security/_authenticate" {
			_, _ = io.WriteString(w, `{"username":"alice"}`)
			return
		}
		_, _ = io.WriteString(w, `{"result":"updated"}`)
	}))
	defer es.Close()

	s := newTestServer(es.URL, "production")
	login := request(http.MethodPost, "/api/clusters/test/login", `{"username":"alice","password":"secret"}`)
	loginRecorder := httptest.NewRecorder()
	s.clusterHandler(loginRecorder, login)
	cookie := loginRecorder.Result().Cookies()[0]
	called = false

	mutation := request(http.MethodPut, "/api/clusters/test/documents/products/abc", `{"document":{"name":"Kibi"}}`)
	mutation.AddCookie(cookie)
	mutationRecorder := httptest.NewRecorder()
	s.clusterHandler(mutationRecorder, mutation)
	if mutationRecorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("mutation status = %d, body = %s", mutationRecorder.Code, mutationRecorder.Body.String())
	}
	if called {
		t.Fatal("production mutation reached Elasticsearch without confirmation")
	}

	confirmed := request(http.MethodPut, "/api/clusters/test/documents/products/abc", `{"document":{"name":"Kibi"},"confirmation":"PRODUCTION"}`)
	confirmed.AddCookie(cookie)
	confirmedRecorder := httptest.NewRecorder()
	s.clusterHandler(confirmedRecorder, confirmed)
	if confirmedRecorder.Code != http.StatusOK {
		t.Fatalf("confirmed mutation status = %d, body = %s", confirmedRecorder.Code, confirmedRecorder.Body.String())
	}
	if !called {
		t.Fatal("confirmed mutation did not reach Elasticsearch")
	}
}

func TestRESTRequestUsesSessionCredentialAndRejectsExternalPaths(t *testing.T) {
	var receivedMethod, receivedPath string
	es := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_security/_authenticate" {
			_, _ = io.WriteString(w, `{"username":"alice"}`)
			return
		}
		receivedMethod, receivedPath = r.Method, r.URL.RequestURI()
		_, _ = io.WriteString(w, `{"hits":{"hits":[]}}`)
	}))
	defer es.Close()

	s := newTestServer(es.URL, "development")
	login := request(http.MethodPost, "/api/clusters/test/login", `{"username":"alice","password":"secret"}`)
	loginRecorder := httptest.NewRecorder()
	s.clusterHandler(loginRecorder, login)
	cookie := loginRecorder.Result().Cookies()[0]

	rest := request(http.MethodPost, "/api/clusters/test/rest", `{"method":"POST","path":"/products/_search?pretty=true","body":{"query":{"match_all":{}}}}`)
	rest.AddCookie(cookie)
	restRecorder := httptest.NewRecorder()
	s.clusterHandler(restRecorder, rest)
	if restRecorder.Code != http.StatusOK {
		t.Fatalf("REST status = %d, body = %s", restRecorder.Code, restRecorder.Body.String())
	}
	if receivedMethod != http.MethodPost || receivedPath != "/products/_search?pretty=true" {
		t.Fatalf("REST request = %s %s", receivedMethod, receivedPath)
	}

	invalid := request(http.MethodPost, "/api/clusters/test/rest", `{"method":"GET","path":"//example.invalid"}`)
	invalid.AddCookie(cookie)
	invalidRecorder := httptest.NewRecorder()
	s.clusterHandler(invalidRecorder, invalid)
	if invalidRecorder.Code != http.StatusBadRequest {
		t.Fatalf("external REST path status = %d, body = %s", invalidRecorder.Code, invalidRecorder.Body.String())
	}
}

func newTestServer(endpoint, environment string) *server {
	return &server{
		clusters: map[string]cluster{"test": {ID: "test", Name: "Test", Endpoint: endpoint, Environment: environment}},
		client:   http.DefaultClient,
		sessions: make(map[string]*session),
	}
}

func request(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}
