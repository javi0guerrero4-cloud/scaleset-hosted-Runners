// Package main implements a stand-in for the GitHub REST API and the GitHub
// Actions runner scale set service.
//
// It exists for the Base44 sandbox: examples/supervisor needs a scale set to
// supervise, and the sandbox has no GitHub credentials. The stand-in keeps its
// state in memory, plays the scheduler that assigns jobs to idle runners, and
// can drive a repeating burst of demand so the live view has something to
// show. It is not a complete GitHub API and it contacts nothing.
package main

import (
	"context"
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

	// pollHold is how long a message poll is held before answering 202.
	pollHold = 2 * time.Second
	// assignDelay is how long an acquired job waits for a runner to come
	// online before the scheduler assigns it, standing in for runner startup.
	assignDelay = 3 * time.Second
	// jobRuntime is how long a simulated job runs before it completes.
	jobRuntime = 4 * time.Second

	stubSigningKey   = "sandbox-signing-key"
	messageQueueRole = "sandbox-session-token"
)

type jobState string

const (
	jobQueued   jobState = "queued"
	jobAcquired jobState = "acquired"
	jobAssigned jobState = "assigned"
)

type job struct {
	id          int
	requestID   int64
	displayName string
	repository  string
	owner       string
	state       jobState
	runnerID    int
	runnerName  string
	queuedAt    time.Time
	acquiredAt  time.Time
}

func (j *job) idString() string {
	return fmt.Sprintf("job-%d", j.id)
}

type runner struct {
	id         int
	name       string
	scaleSetID int
	jobID      string
}

type queuedMessage struct {
	id   int
	body string
}

type scaleSet struct {
	spec      scaleset.RunnerScaleSet
	sessionID uuid.UUID
	messages  []*queuedMessage
	jobs      []*job
	runners   map[int]*runner
	// notify wakes a poll that is waiting for a message.
	notify chan struct{}
}

type stub struct {
	// advertise is the base URL the client is told to call back on.
	advertise string

	mu             sync.Mutex
	nextScaleSetID int
	nextRunnerID   int
	nextMessageID  int
	nextJobID      int
	scaleSets      map[int]*scaleSet
}

func newStub(advertise string) *stub {
	return &stub{
		advertise:      strings.TrimRight(advertise, "/"),
		nextScaleSetID: 1,
		nextRunnerID:   1,
		nextMessageID:  1,
		nextJobID:      1,
		scaleSets:      make(map[int]*scaleSet),
	}
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	// The client asks GitHub for a runner registration token.
	case strings.HasSuffix(r.URL.Path, "/actions/runners/registration-token"):
		writeJSON(w, http.StatusCreated, map[string]any{"token": "sandbox-registration-token"})

	// ... then exchanges it for the Actions tenant URL and an admin JWT.
	case r.URL.Path == apiPrefix+"/actions/runner-registration":
		writeJSON(w, http.StatusCreated, map[string]any{
			"url":   s.advertise + tenantPrefix + "/",
			"token": s.adminToken(),
		})

	case strings.HasPrefix(r.URL.Path, tenantPrefix+"/"):
		s.serveActions(w, r, strings.TrimPrefix(r.URL.Path, tenantPrefix))

	default:
		writeJSON(w, http.StatusNotFound, errorBody("stub: unknown path "+r.URL.Path))
	}
}

func (s *stub) serveActions(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")

	switch {
	case len(parts) >= 3 && parts[0] == "_apis" && parts[1] == "runtime" && parts[2] == "runnergroups":
		s.serveRunnerGroups(w)

	case len(parts) >= 3 && parts[0] == "_apis" && parts[1] == "runtime" && parts[2] == "runnerscalesets":
		s.serveScaleSets(w, r, parts[3:])

	// RemoveRunner: _apis/distributedtask/pools/0/agents/{id}
	case len(parts) == 6 && parts[0] == "_apis" && parts[1] == "distributedtask" && parts[4] == "agents" && r.Method == http.MethodDelete:
		s.removeRunner(w, parts[5])

	default:
		writeJSON(w, http.StatusNotFound, errorBody("stub: no endpoint for "+r.Method+" "+r.URL.Path))
	}
}

func (s *stub) serveRunnerGroups(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, scaleset.RunnerGroupList{
		Count: 1,
		RunnerGroups: []scaleset.RunnerGroup{{
			ID:        1,
			Name:      scaleset.DefaultRunnerGroup,
			Size:      100,
			IsDefault: true,
		}},
	})
}

func (s *stub) serveScaleSets(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			s.listScaleSets(w)
		case http.MethodPost:
			s.createScaleSet(w, r)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, errorBody("stub: unsupported method"))
		}
		return
	}

	if len(rest) == 1 {
		if r.Method == http.MethodDelete {
			s.deleteScaleSet(w, rest[0])
			return
		}
		writeJSON(w, http.StatusNotFound, errorBody("stub: no endpoint for "+r.Method+" "+r.URL.Path))
		return
	}

	scaleSetID, err := strconv.Atoi(rest[0])
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("stub: unknown scale set "+rest[0]))
		return
	}

	s.mu.Lock()
	scaleSet := s.scaleSets[scaleSetID]
	s.mu.Unlock()

	if scaleSet == nil {
		writeJSON(w, http.StatusNotFound, errorBody(fmt.Sprintf("stub: scale set %d not found", scaleSetID)))
		return
	}

	switch action := rest[1]; {
	case action == "sessions" && len(rest) == 2 && r.Method == http.MethodPost:
		s.createSession(w, r, scaleSet)

	case action == "sessions" && len(rest) == 3 && r.Method == http.MethodDelete:
		s.deleteSession(w, scaleSet, rest[2])

	case action == "messages" && len(rest) == 2 && r.Method == http.MethodGet:
		s.pollMessages(w, r, scaleSet)

	case action == "messages" && len(rest) == 3 && r.Method == http.MethodDelete:
		s.deleteMessage(w, scaleSet, rest[2])

	case action == "acquirejobs" && len(rest) == 2 && r.Method == http.MethodPost:
		s.acquireJobs(w, r, scaleSet)

	case action == "generatejitconfig" && len(rest) == 2 && r.Method == http.MethodPost:
		s.generateJitConfig(w, r, scaleSet)

	default:
		writeJSON(w, http.StatusNotFound, errorBody("stub: no endpoint for "+r.Method+" "+r.URL.Path))
	}
}

func (s *stub) createScaleSet(w http.ResponseWriter, r *http.Request) {
	var requested scaleset.RunnerScaleSet
	if err := json.NewDecoder(r.Body).Decode(&requested); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextScaleSetID
	s.nextScaleSetID++

	created := requested
	created.ID = id
	created.CreatedOn = time.Now().UTC()
	created.RunnerJitConfigURL = fmt.Sprintf(
		"%s%s/_apis/runtime/runnerscalesets/%d/generatejitconfig",
		s.advertise, tenantPrefix, id,
	)

	s.scaleSets[id] = &scaleSet{
		spec:    created,
		runners: make(map[int]*runner),
		notify:  make(chan struct{}, 1),
	}

	writeJSON(w, http.StatusOK, created)
}

func (s *stub) listScaleSets(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sets := make([]scaleset.RunnerScaleSet, 0, len(s.scaleSets))
	for _, scaleSet := range s.scaleSets {
		sets = append(sets, scaleSet.spec)
	}

	writeJSON(w, http.StatusOK, map[string]any{"count": len(sets), "value": sets})
}

func (s *stub) deleteScaleSet(w http.ResponseWriter, id string) {
	scaleSetID, err := strconv.Atoi(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("stub: unknown scale set "+id))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.scaleSets, scaleSetID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *stub) createSession(w http.ResponseWriter, r *http.Request, scaleSet *scaleSet) {
	var requested scaleset.RunnerScaleSetSession
	if err := json.NewDecoder(r.Body).Decode(&requested); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	scaleSet.sessionID = uuid.New()

	writeJSON(w, http.StatusOK, scaleset.RunnerScaleSetSession{
		SessionID:               scaleSet.sessionID,
		OwnerName:               requested.OwnerName,
		RunnerScaleSet:          &scaleSet.spec,
		MessageQueueURL:         fmt.Sprintf("%s%s/_apis/runtime/runnerscalesets/%d/messages", s.advertise, tenantPrefix, scaleSet.spec.ID),
		MessageQueueAccessToken: messageQueueRole,
		Statistics:              statisticsLocked(scaleSet),
	})
}

func (s *stub) deleteSession(w http.ResponseWriter, scaleSet *scaleSet, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sessionID != scaleSet.sessionID.String() {
		writeJSON(w, http.StatusNotFound, errorBody("stub: unknown session "+sessionID))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// pollMessages long polls for the next queued message and answers 202 when the
// hold expires, exactly like the real service.
func (s *stub) pollMessages(w http.ResponseWriter, r *http.Request, scaleSet *scaleSet) {
	deadline := time.NewTimer(pollHold)
	defer deadline.Stop()

	for {
		s.mu.Lock()
		if len(scaleSet.messages) > 0 {
			message := scaleSet.messages[0]
			statistics := statisticsLocked(scaleSet)
			s.mu.Unlock()

			writeJSON(w, http.StatusOK, map[string]any{
				"messageId":   message.id,
				"messageType": "RunnerScaleSetJobMessages",
				"body":        message.body,
				"statistics":  statistics,
			})
			return
		}
		notify := scaleSet.notify
		s.mu.Unlock()

		select {
		case <-notify:
		case <-deadline.C:
			w.WriteHeader(http.StatusAccepted)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *stub) deleteMessage(w http.ResponseWriter, scaleSet *scaleSet, id string) {
	messageID, err := strconv.Atoi(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("stub: unknown message "+id))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, message := range scaleSet.messages {
		if message.id == messageID {
			scaleSet.messages = append(scaleSet.messages[:i], scaleSet.messages[i+1:]...)
			break
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *stub) acquireJobs(w http.ResponseWriter, r *http.Request, scaleSet *scaleSet) {
	var requestIDs []int64
	if err := json.NewDecoder(r.Body).Decode(&requestIDs); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	acquired := make([]int64, 0, len(requestIDs))
	for _, requestID := range requestIDs {
		for _, job := range scaleSet.jobs {
			if job.requestID != requestID || job.state != jobQueued {
				continue
			}
			job.state = jobAcquired
			job.acquiredAt = time.Now()
			acquired = append(acquired, requestID)
			break
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"count": len(acquired), "value": acquired})
}

func (s *stub) generateJitConfig(w http.ResponseWriter, r *http.Request, scaleSet *scaleSet) {
	var setting scaleset.RunnerScaleSetJitRunnerSetting
	if err := json.NewDecoder(r.Body).Decode(&setting); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextRunnerID
	s.nextRunnerID++

	// A JIT runner exists as soon as its config does, which is what lets the
	// scheduler hand it a job.
	scaleSet.runners[id] = &runner{id: id, name: setting.Name, scaleSetID: scaleSet.spec.ID}

	writeJSON(w, http.StatusOK, scaleset.RunnerScaleSetJitRunnerConfig{
		Runner: &scaleset.RunnerReference{
			ID:               id,
			Name:             setting.Name,
			RunnerScaleSetID: scaleSet.spec.ID,
		},
		EncodedJITConfig: "c2FuZGJveC1qaXQtcnVubmVyLWNvbmZpZw==",
	})
}

func (s *stub) removeRunner(w http.ResponseWriter, id string) {
	runnerID, err := strconv.Atoi(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody("stub: unknown runner "+id))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, scaleSet := range s.scaleSets {
		delete(scaleSet.runners, runnerID)
	}

	w.WriteHeader(http.StatusNoContent)
}

// runScheduler assigns acquired jobs to idle runners and completes them, which
// is what GitHub does with a scale set's runners.
func (s *stub) runScheduler(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.assignJobs()
		}
	}
}

func (s *stub) assignJobs() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, scaleSet := range s.scaleSets {
		for _, job := range scaleSet.jobs {
			if job.state != jobAcquired || now.Sub(job.acquiredAt) < assignDelay {
				continue
			}

			idle := scaleSet.idleRunnerLocked()
			if idle == nil {
				continue
			}

			job.state = jobAssigned
			job.runnerID = idle.id
			job.runnerName = idle.name
			idle.jobID = job.idString()

			s.enqueueLocked(scaleSet, s.startedBody(job))
			s.notifyLocked(scaleSet)

			scaleSetID, jobID := scaleSet.spec.ID, job.id
			time.AfterFunc(jobRuntime, func() { s.completeJob(scaleSetID, jobID) })

			// Hand out one job per tick, the way jobs are picked up as runners
			// register one at a time.
			break
		}
	}
}

func (s *stub) completeJob(scaleSetID, jobID int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	scaleSet := s.scaleSets[scaleSetID]
	if scaleSet == nil {
		return
	}

	job := scaleSet.jobLocked(jobID)
	if job == nil || job.state != jobAssigned {
		return
	}

	// Runners are ephemeral: they leave after one job.
	delete(scaleSet.runners, job.runnerID)
	scaleSet.removeJobLocked(jobID)

	s.enqueueLocked(scaleSet, s.completedBody(job))
	s.notifyLocked(scaleSet)
}

// enqueueDemand queues a burst of jobs on every scale set. It reports whether
// there was a scale set to queue them on.
func (s *stub) enqueueDemand(count int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	queued := false
	for _, scaleSet := range s.scaleSets {
		queued = true
		jobs := make([]*job, 0, count)
		for range count {
			s.nextJobID++
			jobs = append(jobs, &job{
				id:          s.nextJobID,
				requestID:   int64(200 + s.nextJobID),
				displayName: fmt.Sprintf("build (%d)", s.nextJobID),
				repository:  "demo",
				owner:       "sandbox",
				state:       jobQueued,
				queuedAt:    time.Now().UTC(),
			})
		}

		scaleSet.jobs = append(scaleSet.jobs, jobs...)
		s.enqueueLocked(scaleSet, s.availableBody(scaleSet, jobs))
		s.notifyLocked(scaleSet)
	}

	return queued
}

// cancelDemand drops queued jobs again, the way a user canceling workflow runs
// shrinks demand before any runner picks the work up.
func (s *stub) cancelDemand(count int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, scaleSet := range s.scaleSets {
		remaining := make([]*job, 0, len(scaleSet.jobs))
		canceled := 0

		for _, job := range scaleSet.jobs {
			if canceled < count && job.state != jobAssigned {
				canceled++
				continue
			}
			remaining = append(remaining, job)
		}

		if canceled == 0 {
			continue
		}

		scaleSet.jobs = remaining
		// Nothing but the statistics changed, so the message carries no job
		// messages; GitHub reports the current statistics with every response.
		s.enqueueLocked(scaleSet, "[]")
		s.notifyLocked(scaleSet)
	}
}

func (s *stub) enqueueLocked(scaleSet *scaleSet, body string) {
	scaleSet.messages = append(scaleSet.messages, &queuedMessage{id: s.nextMessageID, body: body})
	s.nextMessageID++
}

func (s *stub) notifyLocked(scaleSet *scaleSet) {
	select {
	case scaleSet.notify <- struct{}{}:
	default:
	}
}

func (s *stub) availableBody(scaleSet *scaleSet, jobs []*job) string {
	available := make([]scaleset.JobAvailable, 0, len(jobs))
	for _, job := range jobs {
		available = append(available, scaleset.JobAvailable{
			AcquireJobURL:  fmt.Sprintf("%s%s/_apis/runtime/runnerscalesets/%d/acquirejobs", s.advertise, tenantPrefix, scaleSet.spec.ID),
			JobMessageBase: baseMessage(job, scaleset.MessageTypeJobAvailable),
		})
	}

	return encodeBody(available)
}

func (s *stub) startedBody(job *job) string {
	return encodeBody([]scaleset.JobStarted{{
		RunnerID:       job.runnerID,
		RunnerName:     job.runnerName,
		JobMessageBase: baseMessage(job, scaleset.MessageTypeJobStarted),
	}})
}

func (s *stub) completedBody(job *job) string {
	base := baseMessage(job, scaleset.MessageTypeJobCompleted)
	base.FinishTime = time.Now().UTC()

	return encodeBody([]scaleset.JobCompleted{{
		Result:         "success",
		RunnerID:       job.runnerID,
		RunnerName:     job.runnerName,
		JobMessageBase: base,
	}})
}

func baseMessage(job *job, messageType scaleset.MessageType) scaleset.JobMessageBase {
	return scaleset.JobMessageBase{
		JobMessageType:  scaleset.JobMessageType{MessageType: messageType},
		RunnerRequestID: job.requestID,
		RepositoryName:  job.repository,
		OwnerName:       job.owner,
		JobID:           job.idString(),
		JobDisplayName:  job.displayName,
		JobWorkflowRef:  "sandbox/demo/.github/workflows/build.yml@refs/heads/main",
		WorkflowRunID:   int64(1000 + job.id),
		EventName:       "push",
		RequestLabels:   []string{"sandbox-supervisor"},
		QueueTime:       job.queuedAt,
	}
}

func encodeBody(payload any) string {
	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Printf("stub: encoding message body: %v", err)
		return "[]"
	}
	return string(encoded)
}

func (scaleSet *scaleSet) idleRunnerLocked() *runner {
	for _, runner := range scaleSet.runners {
		if runner.jobID == "" {
			return runner
		}
	}
	return nil
}

func (scaleSet *scaleSet) jobLocked(id int) *job {
	for _, job := range scaleSet.jobs {
		if job.id == id {
			return job
		}
	}
	return nil
}

func (scaleSet *scaleSet) removeJobLocked(id int) {
	for i, job := range scaleSet.jobs {
		if job.id == id {
			scaleSet.jobs = append(scaleSet.jobs[:i], scaleSet.jobs[i+1:]...)
			return
		}
	}
}

func statisticsLocked(scaleSet *scaleSet) *scaleset.RunnerScaleSetStatistic {
	statistics := &scaleset.RunnerScaleSetStatistic{}

	for _, job := range scaleSet.jobs {
		switch job.state {
		case jobQueued:
			statistics.TotalAvailableJobs++
		case jobAcquired:
			statistics.TotalAcquiredJobs++
		case jobAssigned:
			statistics.TotalRunningJobs++
		}
	}

	statistics.TotalAssignedJobs = statistics.TotalAvailableJobs + statistics.TotalAcquiredJobs + statistics.TotalRunningJobs
	statistics.TotalRegisteredRunners = len(scaleSet.runners)

	for _, runner := range scaleSet.runners {
		if runner.jobID != "" {
			statistics.TotalBusyRunners++
		}
	}
	statistics.TotalIdleRunners = statistics.TotalRegisteredRunners - statistics.TotalBusyRunners

	return statistics
}

// adminToken is the JWT the client parses to learn when the admin token
// expires, so it has to be a well formed token with an exp claim.
func (s *stub) adminToken() string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "sandbox",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	signed, err := token.SignedString([]byte(stubSigningKey))
	if err != nil {
		log.Printf("stub: signing admin token: %v", err)
	}

	return signed
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
