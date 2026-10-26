package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/actions/scaleset"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	// apiPrefix is the GitHub REST API prefix the client derives from a GitHub
	// Enterprise Server style config URL.
	apiPrefix = "/api/v3"
	// tenantPrefix stands in for the URL the runner registration endpoint
	// returns, which every subsequent Actions service call is made against.
	tenantPrefix = "/tenant/1"

	stubSigningKey  = "base44-preview-signing-key"
	stubMessageID   = 1
	stubRequestID   = int64(202)
	stubSessionRole = "stub-session-access-token"
)

// stub stands in for the GitHub REST API and the GitHub Actions service. It
// implements only the endpoints the demo scenario exercises and keeps all of
// its state in memory.
type stub struct {
	baseURL string

	mu        sync.Mutex
	nextID    int
	scaleSets map[int]*stubScaleSet
}

type stubScaleSet struct {
	spec       scaleset.RunnerScaleSet
	sessionID  uuid.UUID
	jobPending bool
}

func newStub(baseURL string) *stub {
	return &stub{
		baseURL:   strings.TrimRight(baseURL, "/"),
		nextID:    1,
		scaleSets: make(map[int]*stubScaleSet),
	}
}

// reset drops all state so every page load runs the same scenario.
func (s *stub) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID = 1
	s.scaleSets = make(map[int]*stubScaleSet)
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	// The client asks GitHub for a runner registration token.
	case strings.HasSuffix(r.URL.Path, "/actions/runners/registration-token"):
		writeJSON(w, http.StatusCreated, map[string]any{"token": "stub-registration-token"})

	// ... then exchanges it for the Actions tenant URL and an admin JWT.
	case r.URL.Path == apiPrefix+"/actions/runner-registration":
		writeJSON(w, http.StatusCreated, map[string]any{
			"url":   s.baseURL + tenantPrefix + "/",
			"token": s.adminToken(),
		})

	case strings.HasPrefix(r.URL.Path, tenantPrefix+"/"):
		s.serveActions(w, r, strings.TrimPrefix(r.URL.Path, tenantPrefix))

	default:
		writeJSON(w, http.StatusNotFound, errorBody(fmt.Sprintf("stub: unknown path %s", r.URL.Path)))
	}
}

func (s *stub) serveActions(w http.ResponseWriter, r *http.Request, path string) {
	// _apis/runtime/runnerscalesets[/{id}[/{action}[/{sub}]]]
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 || parts[0] != "_apis" || parts[1] != "runtime" || parts[2] != "runnerscalesets" {
		writeJSON(w, http.StatusNotFound, errorBody(fmt.Sprintf("stub: unknown path %s", r.URL.Path)))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case len(parts) == 3 && r.Method == http.MethodPost:
		s.createScaleSet(w, r)
		return

	case len(parts) == 3 && r.Method == http.MethodGet:
		s.listScaleSets(w)
		return

	case len(parts) == 4 && r.Method == http.MethodDelete:
		_, id, ok := s.lookup(w, parts[3])
		if !ok {
			return
		}
		delete(s.scaleSets, id)
		w.WriteHeader(http.StatusNoContent)
		return

	case len(parts) == 5:
		ss, id, ok := s.lookup(w, parts[3])
		if !ok {
			return
		}
		switch parts[4] {
		case "sessions":
			if r.Method == http.MethodPost {
				s.createSession(w, r, ss, id)
				return
			}
		case "messages":
			if r.Method == http.MethodGet {
				s.pollMessages(w, ss, id)
				return
			}
		case "acquirejobs":
			if r.Method == http.MethodPost {
				s.acquireJobs(w, r)
				return
			}
		case "generatejitconfig":
			if r.Method == http.MethodPost {
				s.generateJitConfig(w, r, id)
				return
			}
		}

	case len(parts) == 6:
		ss, _, ok := s.lookup(w, parts[3])
		if !ok {
			return
		}
		switch {
		case parts[4] == "sessions" && r.Method == http.MethodDelete:
			if parts[5] != ss.sessionID.String() {
				writeJSON(w, http.StatusNotFound, errorBody("stub: unknown session "+parts[5]))
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		case parts[4] == "messages" && r.Method == http.MethodDelete:
			ss.jobPending = false
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	writeJSON(w, http.StatusNotFound, errorBody(fmt.Sprintf("stub: no endpoint for %s %s", r.Method, r.URL.Path)))
}

func (s *stub) createScaleSet(w http.ResponseWriter, r *http.Request) {
	var requested scaleset.RunnerScaleSet
	if err := json.NewDecoder(r.Body).Decode(&requested); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}

	id := s.nextID
	s.nextID++

	created := requested
	created.ID = id
	created.CreatedOn = time.Now().UTC()
	created.RunnerJitConfigURL = fmt.Sprintf(
		"%s%s/_apis/runtime/runnerscalesets/%d/generatejitconfig",
		s.baseURL, tenantPrefix, id,
	)

	s.scaleSets[id] = &stubScaleSet{spec: created}

	writeJSON(w, http.StatusOK, created)
}

func (s *stub) listScaleSets(w http.ResponseWriter) {
	sets := make([]scaleset.RunnerScaleSet, 0, len(s.scaleSets))
	for _, ss := range s.scaleSets {
		sets = append(sets, ss.spec)
	}

	writeJSON(w, http.StatusOK, map[string]any{"count": len(sets), "value": sets})
}

func (s *stub) createSession(w http.ResponseWriter, r *http.Request, ss *stubScaleSet, id int) {
	var requested scaleset.RunnerScaleSetSession
	if err := json.NewDecoder(r.Body).Decode(&requested); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}

	ss.sessionID = uuid.New()
	ss.jobPending = true

	writeJSON(w, http.StatusOK, scaleset.RunnerScaleSetSession{
		SessionID:               ss.sessionID,
		OwnerName:               requested.OwnerName,
		RunnerScaleSet:          &ss.spec,
		MessageQueueURL:         fmt.Sprintf("%s%s/_apis/runtime/runnerscalesets/%d/messages", s.baseURL, tenantPrefix, id),
		MessageQueueAccessToken: stubSessionRole,
		Statistics:              statistics(1, 0),
	})
}

// pollMessages returns one queued job message and reports an empty queue with
// 202 Accepted afterwards, exactly like the real API.
func (s *stub) pollMessages(w http.ResponseWriter, ss *stubScaleSet, id int) {
	if !ss.jobPending {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	ss.jobPending = false

	batched, err := json.Marshal([]map[string]any{{
		"messageType":     string(scaleset.MessageTypeJobAvailable),
		"runnerRequestId": stubRequestID,
		"repositoryName":  "scaleset",
		"ownerName":       "myorg",
		"jobId":           "job-202",
		"jobDisplayName":  "demo (ubuntu-latest)",
		"jobWorkflowRef":  "actions/scaleset/.github/workflows/demo.yml@refs/heads/main",
		"workflowRunId":   101,
		"eventName":       "workflow_dispatch",
		"requestLabels":   []string{"base44-preview"},
		"queueTime":       time.Now().UTC().Format(time.RFC3339),
		"acquireJobUrl":   fmt.Sprintf("%s%s/_apis/runtime/runnerscalesets/%d/acquirejobs", s.baseURL, tenantPrefix, id),
	}})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorBody(err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"messageId":   stubMessageID,
		"messageType": "RunnerScaleSetJobMessages",
		"body":        string(batched),
		"statistics":  statistics(1, 0),
	})
}

func (s *stub) acquireJobs(w http.ResponseWriter, r *http.Request) {
	var requestIDs []int64
	if err := json.NewDecoder(r.Body).Decode(&requestIDs); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"count": len(requestIDs), "value": requestIDs})
}

func (s *stub) generateJitConfig(w http.ResponseWriter, r *http.Request, id int) {
	var setting scaleset.RunnerScaleSetJitRunnerSetting
	if err := json.NewDecoder(r.Body).Decode(&setting); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, scaleset.RunnerScaleSetJitRunnerConfig{
		Runner:           &scaleset.RunnerReference{ID: 7, Name: setting.Name, RunnerScaleSetID: id},
		EncodedJITConfig: "c3R1Yi1qaXQtcnVubmVyLWNvbmZpZw==",
	})
}

// lookup resolves the scale set id from the request path, or writes a 404.
func (s *stub) lookup(w http.ResponseWriter, id string) (*stubScaleSet, int, bool) {
	scaleSetID, err := strconv.Atoi(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("stub: unknown scale set "+id))
		return nil, 0, false
	}

	ss, ok := s.scaleSets[scaleSetID]
	if !ok {
		writeJSON(w, http.StatusNotFound, errorBody(fmt.Sprintf("stub: scale set %d not found", scaleSetID)))
		return nil, 0, false
	}

	return ss, scaleSetID, true
}

// adminToken is the JWT the client parses to learn when the admin token
// expires, so it has to be a well formed token with an exp claim.
func (s *stub) adminToken() string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "base44-preview",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	signed, err := token.SignedString([]byte(stubSigningKey))
	if err != nil {
		log.Printf("stub: signing admin token: %v", err)
	}

	return signed
}

func statistics(available, acquired int) *scaleset.RunnerScaleSetStatistic {
	return &scaleset.RunnerScaleSetStatistic{
		TotalAvailableJobs: available,
		TotalAcquiredJobs:  acquired,
		TotalAssignedJobs:  available + acquired,
	}
}

func errorBody(message string) map[string]any {
	return map[string]any{"message": message}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("stub: writing response: %v", err)
	}
}
