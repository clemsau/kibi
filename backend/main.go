package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	maxRequestBytes  = 5 << 20
	maxResponseBytes = 12 << 20
	sessionCookie    = "kibi_session"
)

type cluster struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Environment string `json:"environment"`
}

type credential struct {
	Username string
	Secret   string
	AuthType string
}

type auditEvent struct {
	At         time.Time `json:"at"`
	ClusterID  string    `json:"clusterId"`
	Principal  string    `json:"principal"`
	Action     string    `json:"action"`
	Target     string    `json:"target"`
	StatusCode int       `json:"statusCode"`
}

type session struct {
	Credentials map[string]credential
	Audit       []auditEvent
}

type server struct {
	clusters map[string]cluster
	client   *http.Client
	mu       sync.RWMutex
	sessions map[string]*session
}

func main() {
	clusters, err := loadClusters()
	if err != nil {
		log.Fatalf("invalid cluster configuration: %v", err)
	}
	s := &server{
		clusters: clusters,
		client:   &http.Client{Timeout: 20 * time.Second},
		sessions: make(map[string]*session),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.health)
	mux.HandleFunc("/api/clusters", s.clustersHandler)
	mux.HandleFunc("/api/clusters/", s.clusterHandler)
	mux.HandleFunc("/api/audit", s.auditHandler)

	port := os.Getenv("KIBI_PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Kibi API listening on http://localhost:%s with %d configured clusters", port, len(clusters))
	log.Fatal(http.ListenAndServe(":"+port, withCORS(mux)))
}

func loadClusters() (map[string]cluster, error) {
	raw := os.Getenv("KIBI_CLUSTERS")
	if raw == "" {
		raw = `[
  {"id":"dev-local","name":"Development (local)","endpoint":"http://localhost:9200","environment":"development"},
  {"id":"prod-local","name":"Production (local)","endpoint":"http://localhost:9201","environment":"production"}
]`
	}
	var entries []cluster
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, err
	}
	clusters := make(map[string]cluster, len(entries))
	for _, entry := range entries {
		endpoint, err := url.ParseRequestURI(entry.Endpoint)
		if err != nil || endpoint.Scheme == "" || endpoint.Host == "" || entry.ID == "" || entry.Name == "" {
			return nil, fmt.Errorf("cluster %q must provide id, name, and an absolute endpoint", entry.ID)
		}
		if _, exists := clusters[entry.ID]; exists {
			return nil, fmt.Errorf("duplicate cluster id %q", entry.ID)
		}
		entry.Endpoint = strings.TrimRight(entry.Endpoint, "/")
		entry.Environment = strings.ToLower(entry.Environment)
		clusters[entry.ID] = entry
	}
	return clusters, nil
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) clustersHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/clusters" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	sessionID := readSessionID(r)
	s.mu.RLock()
	active := map[string]credential{}
	if stored := s.sessions[sessionID]; stored != nil {
		for clusterID, credential := range stored.Credentials {
			active[clusterID] = credential
		}
	}
	entries := make([]map[string]any, 0, len(s.clusters))
	for _, entry := range s.clusters {
		item := map[string]any{"id": entry.ID, "name": entry.Name, "environment": entry.Environment, "connected": false}
		if credential, connected := active[entry.ID]; connected {
			item["connected"] = true
			item["username"] = credential.Username
			item["authType"] = credential.AuthType
		}
		entries = append(entries, item)
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"clusters": entries})
}

func (s *server) clusterHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/clusters/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	clusterID := parts[0]
	if _, ok := s.clusters[clusterID]; !ok {
		writeError(w, http.StatusNotFound, "unknown cluster")
		return
	}

	if len(parts) == 2 && parts[1] == "login" && r.Method == http.MethodPost {
		s.login(w, r, clusterID)
		return
	}
	if len(parts) == 2 && parts[1] == "session" && r.Method == http.MethodDelete {
		s.disconnect(w, r, clusterID)
		return
	}
	if len(parts) == 2 && parts[1] == "overview" && r.Method == http.MethodGet {
		s.overview(w, r, clusterID)
		return
	}
	if len(parts) == 2 && parts[1] == "indices" && r.Method == http.MethodGet {
		s.forward(w, r, clusterID, http.MethodGet, "/_cat/indices?format=json&bytes=mb&s=index&h=health,status,index,uuid,pri,rep,docs.count,docs.deleted,store.size,store.total_size", nil, "indices")
		return
	}
	if len(parts) == 2 && parts[1] == "nodes" && r.Method == http.MethodGet {
		s.forward(w, r, clusterID, http.MethodGet, "/_cat/nodes?format=json&bytes=mb&full_id=true&h=name,ip,id,role,master,version,cpu,ram.percent,heap.percent,heap.max,ram.max,disk.used_percent,disk.used,disk.avail,load_1m,load_5m,load_15m,uptime", nil, "nodes")
		return
	}
	if len(parts) == 2 && parts[1] == "shards" && r.Method == http.MethodGet {
		s.forward(w, r, clusterID, http.MethodGet, "/_cat/shards?format=json&bytes=mb&s=index,shard&h=index,shard,prirep,state,docs,store,ip,node,unassigned.reason", nil, "shards")
		return
	}
	if len(parts) == 4 && parts[1] == "indices" && parts[3] == "details" && r.Method == http.MethodGet {
		s.indexDetails(w, r, clusterID, parts[2])
		return
	}
	if len(parts) == 3 && parts[1] == "indices" && r.Method == http.MethodGet {
		s.forward(w, r, clusterID, http.MethodGet, "/"+url.PathEscape(parts[2])+"/_mapping", nil, "mapping")
		return
	}
	if len(parts) == 2 && parts[1] == "search" && r.Method == http.MethodPost {
		var input struct {
			Index string          `json:"index"`
			Query json.RawMessage `json:"query"`
		}
		if err := decodeJSON(r, &input); err != nil || len(input.Query) == 0 {
			writeError(w, http.StatusBadRequest, "search requires an index and JSON query")
			return
		}
		index := input.Index
		if index == "" {
			index = "_all"
		}
		s.forward(w, r, clusterID, http.MethodPost, "/"+url.PathEscape(index)+"/_search", input.Query, "search")
		return
	}
	if len(parts) == 2 && parts[1] == "rest" && r.Method == http.MethodPost {
		s.rest(w, r, clusterID)
		return
	}
	if len(parts) == 4 && parts[1] == "documents" {
		if r.Method == http.MethodGet {
			s.forward(w, r, clusterID, http.MethodGet, "/"+url.PathEscape(parts[2])+"/_doc/"+url.PathEscape(parts[3]), nil, "get document")
			return
		}
		if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
			s.documentMutation(w, r, clusterID, parts[2], parts[3])
			return
		}
	}
	http.NotFound(w, r)
}

func (s *server) indexDetails(w http.ResponseWriter, r *http.Request, clusterID, index string) {
	credential, stored, ok := s.credentialFor(r, clusterID)
	if !ok {
		writeError(w, http.StatusUnauthorized, "sign in to this cluster first")
		return
	}
	escapedIndex := url.PathEscape(index)
	requests := []struct {
		name string
		path string
	}{
		{name: "mapping", path: "/" + escapedIndex + "/_mapping"},
		{name: "settings", path: "/" + escapedIndex + "/_settings"},
		{name: "stats", path: "/" + escapedIndex + "/_stats/docs,store"},
		{name: "aliases", path: "/" + escapedIndex + "/_alias"},
	}
	result := make(map[string]json.RawMessage, len(requests))
	for _, detail := range requests {
		payload, status, err := s.esRequest(r, clusterID, credential, http.MethodGet, detail.path, nil)
		if err != nil {
			writeProxyError(w, err)
			return
		}
		s.record(r, clusterID, stored, "index details", detail.path, status)
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(payload)
			return
		}
		result[detail.name] = payload
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) rest(w http.ResponseWriter, r *http.Request, clusterID string) {
	var input struct {
		Method       string          `json:"method"`
		Path         string          `json:"path"`
		Body         json.RawMessage `json:"body"`
		Confirmation string          `json:"confirmation"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "a valid REST request is required")
		return
	}
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodPost && method != http.MethodPut && method != http.MethodDelete {
		writeError(w, http.StatusBadRequest, "REST supports GET, HEAD, POST, PUT, and DELETE")
		return
	}
	path, err := restPath(input.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.clusters[clusterID].Environment == "production" && method != http.MethodGet && method != http.MethodHead && input.Confirmation != "PRODUCTION" {
		writeError(w, http.StatusPreconditionRequired, "type PRODUCTION to run a mutating REST request against production")
		return
	}
	body := []byte(input.Body)
	if string(body) == "null" {
		body = nil
	}
	s.forward(w, r, clusterID, method, path, body, "REST "+method)
}

func restPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "", errors.New("REST path must begin with a single /")
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || strings.Contains(parsed.Path, "..") {
		return "", errors.New("REST path is not valid")
	}
	return parsed.RequestURI(), nil
}

func auditTarget(path string) string {
	parsed, err := url.ParseRequestURI(path)
	if err == nil && parsed.Path != "" {
		return parsed.Path
	}
	return path
}

func (s *server) login(w http.ResponseWriter, r *http.Request, clusterID string) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		APIKey   string `json:"apiKey"`
		AuthType string `json:"authType"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "valid login details are required")
		return
	}
	if input.AuthType == "" {
		input.AuthType = "basic"
	}
	if input.AuthType == "basic" && (input.Username == "" || input.Password == "") {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	if input.AuthType == "api_key" && input.APIKey == "" {
		writeError(w, http.StatusBadRequest, "an Elasticsearch API key is required")
		return
	}
	if input.AuthType != "basic" && input.AuthType != "api_key" {
		writeError(w, http.StatusBadRequest, "unsupported authentication type")
		return
	}
	entry := s.clusters[clusterID]
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, entry.Endpoint+"/_security/_authenticate", nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create authentication request")
		return
	}
	loginCredential := credential{Username: input.Username, Secret: input.Password, AuthType: input.AuthType}
	if input.AuthType == "api_key" {
		loginCredential.Secret = input.APIKey
	}
	applyCredential(request, loginCredential)
	response, err := s.client.Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not reach Elasticsearch")
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeError(w, http.StatusUnauthorized, "Elasticsearch rejected those credentials")
		return
	}
	var authenticated struct {
		Username string `json:"username"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&authenticated)
	if authenticated.Username == "" {
		if input.AuthType == "api_key" {
			authenticated.Username = "API key"
		} else {
			authenticated.Username = input.Username
		}
	}

	sessionID := readSessionID(r)
	if sessionID == "" {
		sessionID = newSessionID()
		secureCookie := os.Getenv("KIBI_COOKIE_SECURE") == "true"
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sessionID, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureCookie, MaxAge: 8 * 60 * 60})
	}
	s.mu.Lock()
	stored := s.sessions[sessionID]
	if stored == nil {
		stored = &session{Credentials: make(map[string]credential)}
		s.sessions[sessionID] = stored
	}
	loginCredential.Username = authenticated.Username
	stored.Credentials[clusterID] = loginCredential
	s.appendAuditLocked(stored, auditEvent{At: time.Now().UTC(), ClusterID: clusterID, Principal: authenticated.Username, Action: "login", Target: "_security/_authenticate", StatusCode: response.StatusCode})
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"clusterId": clusterID, "username": authenticated.Username, "authType": input.AuthType})
}

func (s *server) disconnect(w http.ResponseWriter, r *http.Request, clusterID string) {
	sessionID := readSessionID(r)
	s.mu.Lock()
	if stored := s.sessions[sessionID]; stored != nil {
		delete(stored.Credentials, clusterID)
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) overview(w http.ResponseWriter, r *http.Request, clusterID string) {
	credential, stored, ok := s.credentialFor(r, clusterID)
	if !ok {
		writeError(w, http.StatusUnauthorized, "sign in to this cluster first")
		return
	}
	info, status, err := s.esRequest(r, clusterID, credential, http.MethodGet, "/", nil)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	health, healthStatus, err := s.esRequest(r, clusterID, credential, http.MethodGet, "/_cluster/health", nil)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.record(r, clusterID, stored, "overview", "/", status)
	s.record(r, clusterID, stored, "cluster health", "/_cluster/health", healthStatus)
	writeJSON(w, http.StatusOK, map[string]json.RawMessage{"info": info, "health": health})
}

func (s *server) documentMutation(w http.ResponseWriter, r *http.Request, clusterID, index, documentID string) {
	entry := s.clusters[clusterID]
	var input struct {
		Document      json.RawMessage `json:"document"`
		IfSeqNo       *int64          `json:"if_seq_no"`
		IfPrimaryTerm *int64          `json:"if_primary_term"`
		Confirmation  string          `json:"confirmation"`
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		if err := decodeJSON(r, &input); err != nil || len(input.Document) == 0 {
			writeError(w, http.StatusBadRequest, "a document JSON body is required")
			return
		}
	} else if err := decodeJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid delete request")
		return
	}
	if entry.Environment == "production" && input.Confirmation != "PRODUCTION" {
		writeError(w, http.StatusPreconditionRequired, "type PRODUCTION to mutate a production document")
		return
	}
	path := "/" + url.PathEscape(index) + "/_doc/" + url.PathEscape(documentID)
	body := input.Document
	if r.Method == http.MethodPost {
		path = "/" + url.PathEscape(index) + "/_update/" + url.PathEscape(documentID)
		body, _ = json.Marshal(map[string]json.RawMessage{"doc": input.Document})
	}
	values := url.Values{}
	if input.IfSeqNo != nil {
		values.Set("if_seq_no", fmt.Sprintf("%d", *input.IfSeqNo))
	}
	if input.IfPrimaryTerm != nil {
		values.Set("if_primary_term", fmt.Sprintf("%d", *input.IfPrimaryTerm))
	}
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	action := "delete document"
	if r.Method == http.MethodPut {
		action = "replace document"
	} else if r.Method == http.MethodPost {
		action = "update document"
	}
	s.forward(w, r, clusterID, r.Method, path, body, action)
}

func (s *server) forward(w http.ResponseWriter, r *http.Request, clusterID, method, path string, body []byte, action string) {
	credential, stored, ok := s.credentialFor(r, clusterID)
	if !ok {
		writeError(w, http.StatusUnauthorized, "sign in to this cluster first")
		return
	}
	payload, status, err := s.esRequest(r, clusterID, credential, method, path, body)
	if err != nil {
		writeProxyError(w, err)
		return
	}
	s.record(r, clusterID, stored, action, auditTarget(path), status)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func (s *server) esRequest(r *http.Request, clusterID string, credential credential, method, path string, body []byte) ([]byte, int, error) {
	entry := s.clusters[clusterID]
	request, err := http.NewRequestWithContext(r.Context(), method, entry.Endpoint+path, strings.NewReader(string(body)))
	if err != nil {
		return nil, 0, err
	}
	applyCredential(request, credential)
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if len(payload) > maxResponseBytes {
		return nil, response.StatusCode, fmt.Errorf("Elasticsearch response exceeds the %d MB limit", maxResponseBytes>>20)
	}
	return payload, response.StatusCode, nil
}

func applyCredential(request *http.Request, credential credential) {
	if credential.AuthType == "api_key" {
		request.Header.Set("Authorization", "ApiKey "+credential.Secret)
		return
	}
	request.SetBasicAuth(credential.Username, credential.Secret)
}

func (s *server) credentialFor(r *http.Request, clusterID string) (credential, *session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stored := s.sessions[readSessionID(r)]
	if stored == nil {
		return credential{}, nil, false
	}
	value, ok := stored.Credentials[clusterID]
	return value, stored, ok
}

func (s *server) record(r *http.Request, clusterID string, stored *session, action, target string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if stored == nil {
		return
	}
	principal := stored.Credentials[clusterID].Username
	s.appendAuditLocked(stored, auditEvent{At: time.Now().UTC(), ClusterID: clusterID, Principal: principal, Action: action, Target: target, StatusCode: status})
}

func (s *server) appendAuditLocked(stored *session, event auditEvent) {
	stored.Audit = append([]auditEvent{event}, stored.Audit...)
	if len(stored.Audit) > 100 {
		stored.Audit = stored.Audit[:100]
	}
}

func (s *server) auditHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	s.mu.RLock()
	stored := s.sessions[readSessionID(r)]
	if stored == nil {
		s.mu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]any{"events": []auditEvent{}})
		return
	}
	events := append([]auditEvent(nil), stored.Audit...)
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxRequestBytes {
		return errors.New("request body exceeds the allowed limit")
	}
	return json.Unmarshal(payload, target)
}

func readSessionID(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func newSessionID() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "http://localhost:5173" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeProxyError(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "response exceeds") {
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "Elasticsearch request failed")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
