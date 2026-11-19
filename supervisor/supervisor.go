// Package supervisor runs a GitHub Actions runner scale set.
//
// It handles the messages the listener delivers, acquires the jobs GitHub
// offers, provisions runners with just-in-time configs while there is demand
// and retires runners that are no longer needed. Everything it does is kept in
// memory and exposed through Snapshot, which the live view renders.
//
// The encoded JIT config is a runner's credential. Config.Launch is called with
// it so the caller can start the runner (a container, a VM, a process). When
// Launch is nil the runner is only tracked, which is what a scale set with an
// external launcher - or the sandbox stand-in for GitHub's API - needs.
package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/actions/scaleset"
)

const (
	// DefaultRunnerNamePrefix is the name prefix used when Config leaves
	// RunnerNamePrefix empty.
	DefaultRunnerNamePrefix = "scaleset-runner"
	// DefaultWorkFolder is the runner work folder used when Config leaves
	// WorkFolder empty.
	DefaultWorkFolder = "_work"

	// eventLimit is how many events the live view keeps.
	eventLimit = 60
	// doneRunnerLimit is how many finished runners stay in the snapshot.
	doneRunnerLimit = 10
)

// RunnerState is where a runner is in its (short) life.
type RunnerState string

const (
	// StatePending means the JIT config exists but no job has started yet.
	StatePending RunnerState = "pending"
	// StateBusy means GitHub reported a job started on the runner.
	StateBusy RunnerState = "busy"
	// StateDone means the runner's job finished. Ephemeral runners exit after
	// one job, so this is history rather than capacity.
	StateDone RunnerState = "done"
)

// Runner is one runner this supervisor provisioned.
type Runner struct {
	Name           string      `json:"name"`
	ID             int         `json:"id"`
	State          RunnerState `json:"state"`
	JobID          string      `json:"jobId,omitempty"`
	JobDisplayName string      `json:"jobDisplayName,omitempty"`
	CreatedAt      time.Time   `json:"createdAt"`
	StartedAt      time.Time   `json:"startedAt,omitzero"`
	FinishedAt     time.Time   `json:"finishedAt,omitzero"`
}

// Event is one thing the supervisor did or saw, newest first in Snapshot.
type Event struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
}

// JobAcquirer acquires the jobs GitHub offers. *scaleset.MessageSessionClient
// implements it.
type JobAcquirer interface {
	AcquireJobs(ctx context.Context, requestIDs []int64) ([]int64, error)
}

// Provisioner creates and removes runners. *scaleset.Client implements it.
type Provisioner interface {
	GenerateJitRunnerConfig(ctx context.Context, setting *scaleset.RunnerScaleSetJitRunnerSetting, scaleSetID int) (*scaleset.RunnerScaleSetJitRunnerConfig, error)
	RemoveRunner(ctx context.Context, runnerID int64) error
}

// Launcher starts a runner from its JIT config. It is called once per
// provisioned runner and may block for as long as the runner lives.
type Launcher func(ctx context.Context, runner Runner, jitConfig string) error

// Config configures a Supervisor.
type Config struct {
	// ScaleSetID is the runner scale set to supervise. Required.
	ScaleSetID int
	// ScaleSetName is only used by the live view.
	ScaleSetName string
	// MinRunners are kept running when there is no demand.
	MinRunners int
	// MaxRunners is the capacity reported to GitHub while polling.
	MaxRunners int
	// RunnerNamePrefix names the provisioned runners.
	RunnerNamePrefix string
	// WorkFolder is the work folder handed to each new runner.
	WorkFolder string
	// Launch starts a provisioned runner. Nil only tracks runners.
	Launch Launcher
	// Logger receives the supervisor's own log lines. Default is a no-op.
	Logger *slog.Logger
}

func (c *Config) defaults() {
	if c.RunnerNamePrefix == "" {
		c.RunnerNamePrefix = DefaultRunnerNamePrefix
	}
	if c.WorkFolder == "" {
		c.WorkFolder = DefaultWorkFolder
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
}

// Validate returns an error if the configuration cannot be used.
func (c *Config) Validate() error {
	c.defaults()

	if c.ScaleSetID == 0 {
		return errors.New("scaleSetID is required")
	}
	if c.MinRunners < 0 {
		return errors.New("minRunners must not be negative")
	}
	if c.MaxRunners < 0 {
		return errors.New("maxRunners must not be negative")
	}
	if c.MinRunners > c.MaxRunners {
		return errors.New("minRunners must not exceed maxRunners")
	}
	return nil
}

// Snapshot is the state the live view renders.
type Snapshot struct {
	ScaleSetName       string                            `json:"scaleSetName"`
	ScaleSetID         int                               `json:"scaleSetId"`
	MinRunners         int                               `json:"minRunners"`
	MaxRunners         int                               `json:"maxRunners"`
	DesiredRunners     int                               `json:"desiredRunners"`
	ActiveRunners      int                               `json:"activeRunners"`
	BusyRunners        int                               `json:"busyRunners"`
	PendingRunners     int                               `json:"pendingRunners"`
	ProvisionedRunners int                               `json:"provisionedRunners"`
	RetiredRunners     int                               `json:"retiredRunners"`
	Polls              int                               `json:"polls"`
	StartedAt          time.Time                         `json:"startedAt"`
	LastPollAt         time.Time                         `json:"lastPollAt,omitzero"`
	Statistics         *scaleset.RunnerScaleSetStatistic `json:"statistics,omitempty"`
	Runners            []Runner                          `json:"runners"`
	Events             []Event                           `json:"events"`
}

// Supervisor implements listener.Scaler for one runner scale set.
type Supervisor struct {
	acquirer    JobAcquirer
	provisioner Provisioner
	config      Config
	logger      *slog.Logger

	mu          sync.Mutex
	runners     map[string]*Runner
	events      []Event
	statistics  *scaleset.RunnerScaleSetStatistic
	desired     int
	startedAt   time.Time
	lastPollAt  time.Time
	polls       int
	provisioned int
	retired     int
	nextRunner  int
	// nameSuffix keeps runner names unique across restarts: a runner the
	// previous process left registered must not clash with a new one.
	nameSuffix string
}

// New creates a Supervisor for the scale set described by config.
func New(acquirer JobAcquirer, provisioner Provisioner, config Config) (*Supervisor, error) {
	if acquirer == nil {
		return nil, errors.New("acquirer is required")
	}
	if provisioner == nil {
		return nil, errors.New("provisioner is required")
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &Supervisor{
		acquirer:    acquirer,
		provisioner: provisioner,
		config:      config,
		logger:      config.Logger,
		runners:     make(map[string]*Runner),
		startedAt:   time.Now(),
		nameSuffix:  runnerNameSuffix(),
	}, nil
}

func runnerNameSuffix() string {
	var buffer [2]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return fmt.Sprintf("%04x", time.Now().UnixNano()&0xffff)
	}
	return hex.EncodeToString(buffer[:])
}

// Scale handles one scale set message. The listener calls it once per poll,
// including once with the session statistics before polling starts.
func (s *Supervisor) Scale(ctx context.Context, message *scaleset.RunnerScaleSetMessage) error {
	s.recordPoll()

	if message == nil {
		// The long poll timed out. GitHub reports the state with every
		// message, so there is nothing to reconcile until the next one.
		return nil
	}

	if message.Statistics != nil {
		s.setStatistics(message.Statistics)
	}

	if len(message.JobAvailableMessages) > 0 {
		if err := s.acquireJobs(ctx, message.JobAvailableMessages); err != nil {
			return err
		}
	}

	for _, started := range message.JobStartedMessages {
		s.jobStarted(started)
	}
	for _, completed := range message.JobCompletedMessages {
		s.jobCompleted(completed)
	}

	if message.Statistics == nil {
		return nil
	}

	return s.reconcile(ctx, message.Statistics.TotalAssignedJobs)
}

func (s *Supervisor) acquireJobs(ctx context.Context, jobs []*scaleset.JobAvailable) error {
	requestIDs := make([]int64, 0, len(jobs))
	for _, job := range jobs {
		requestIDs = append(requestIDs, job.RunnerRequestID)
	}

	acquired, err := s.acquirer.AcquireJobs(ctx, requestIDs)
	if err != nil {
		s.addEvent("job", fmt.Sprintf("could not acquire %d job(s): %v", len(requestIDs), err))
		return fmt.Errorf("acquire jobs: %w", err)
	}

	s.addEvent("job", fmt.Sprintf("acquired %d of %d available job(s)", len(acquired), len(requestIDs)))
	return nil
}

func (s *Supervisor) jobStarted(job *scaleset.JobStarted) {
	s.mu.Lock()
	defer s.mu.Unlock()

	runner, ok := s.runners[job.RunnerName]
	if !ok {
		// The runner picked up a job after this supervisor stopped tracking it:
		// it was retired just as GitHub assigned the work, or another
		// supervisor owns it. Track it while it runs so the counts and the live
		// view stay truthful.
		runner = &Runner{Name: job.RunnerName, ID: job.RunnerID, CreatedAt: time.Now()}
		s.runners[job.RunnerName] = runner
	}

	runner.State = StateBusy
	runner.JobID = job.JobID
	runner.JobDisplayName = job.JobDisplayName
	runner.StartedAt = time.Now()

	s.addEventLocked("job", fmt.Sprintf("job %s started on %s", job.JobID, runner.Name))
}

func (s *Supervisor) jobCompleted(job *scaleset.JobCompleted) {
	s.mu.Lock()
	defer s.mu.Unlock()

	runner, ok := s.runners[job.RunnerName]
	if !ok {
		return
	}

	runner.State = StateDone
	runner.FinishedAt = time.Now()

	message := fmt.Sprintf("job %s finished on %s", job.JobID, runner.Name)
	if job.Result != "" {
		message += " (" + job.Result + ")"
	}
	s.addEventLocked("job", message)

	s.pruneDoneLocked()
}

// reconcile converges on the number of runners the current demand needs.
func (s *Supervisor) reconcile(ctx context.Context, assignedJobs int) error {
	desired := s.desiredRunners(assignedJobs)
	s.setDesired(desired)

	for s.activeRunners() < desired {
		if err := s.provision(ctx); err != nil {
			return err
		}
	}

	for s.activeRunners() > desired {
		retired, err := s.retire(ctx)
		if err != nil {
			return err
		}
		if !retired {
			// Every remaining runner has a job. The next message scales down.
			break
		}
	}

	return nil
}

func (s *Supervisor) desiredRunners(assignedJobs int) int {
	desired := s.config.MinRunners + assignedJobs
	if desired > s.config.MaxRunners {
		desired = s.config.MaxRunners
	}
	if desired < 0 {
		desired = 0
	}
	return desired
}

func (s *Supervisor) provision(ctx context.Context) error {
	s.mu.Lock()
	s.nextRunner++
	name := fmt.Sprintf("%s-%s-%d", s.config.RunnerNamePrefix, s.nameSuffix, s.nextRunner)
	s.mu.Unlock()

	setting := &scaleset.RunnerScaleSetJitRunnerSetting{
		Name:       name,
		WorkFolder: s.config.WorkFolder,
	}

	jitConfig, err := s.provisioner.GenerateJitRunnerConfig(ctx, setting, s.config.ScaleSetID)
	if err != nil {
		s.addEvent("runner", fmt.Sprintf("could not provision %s: %v", name, err))
		return fmt.Errorf("provision runner %q: %w", name, err)
	}

	runner := &Runner{Name: name, State: StatePending, CreatedAt: time.Now()}
	if jitConfig.Runner != nil {
		runner.ID = jitConfig.Runner.ID
	}

	s.mu.Lock()
	s.runners[name] = runner
	s.provisioned++
	s.addEventLocked("runner", fmt.Sprintf("provisioned %s (runner %d)", name, runner.ID))
	launch := s.config.Launch
	s.mu.Unlock()

	s.logger.Info("provisioned runner", "runner", name, "id", runner.ID)

	if launch != nil {
		go s.launch(ctx, *runner, jitConfig.EncodedJITConfig)
	}

	return nil
}

func (s *Supervisor) launch(ctx context.Context, runner Runner, jitConfig string) {
	if err := s.config.Launch(ctx, runner, jitConfig); err != nil {
		s.addEvent("runner", fmt.Sprintf("%s could not start: %v", runner.Name, err))
		s.forget(runner.Name)
		return
	}

	s.addEvent("runner", fmt.Sprintf("%s exited", runner.Name))
}

// retire removes the oldest runner that has no job yet.
func (s *Supervisor) retire(ctx context.Context) (bool, error) {
	s.mu.Lock()
	var oldest *Runner
	for _, runner := range s.runners {
		if runner.State != StatePending {
			continue
		}
		if oldest == nil || runner.CreatedAt.Before(oldest.CreatedAt) {
			oldest = runner
		}
	}
	s.mu.Unlock()

	if oldest == nil {
		return false, nil
	}

	if err := s.provisioner.RemoveRunner(ctx, int64(oldest.ID)); err != nil {
		// A runner that never registered is already gone as far as GitHub is
		// concerned. Dropping it here as well keeps the supervisor converging
		// on the target instead of retrying a runner nothing will use.
		s.addEvent("runner", fmt.Sprintf("could not remove %s: %v", oldest.Name, err))
	} else {
		s.addEvent("runner", fmt.Sprintf("retired %s (runner %d)", oldest.Name, oldest.ID))
	}

	s.mu.Lock()
	delete(s.runners, oldest.Name)
	s.retired++
	s.mu.Unlock()

	return true, nil
}

func (s *Supervisor) forget(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.runners, name)
}

func (s *Supervisor) activeRunners() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	active := 0
	for _, runner := range s.runners {
		if runner.State != StateDone {
			active++
		}
	}
	return active
}

func (s *Supervisor) recordPoll() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.polls++
	s.lastPollAt = time.Now()
}

func (s *Supervisor) setStatistics(statistics *scaleset.RunnerScaleSetStatistic) {
	s.mu.Lock()
	defer s.mu.Unlock()

	copied := *statistics
	s.statistics = &copied
}

func (s *Supervisor) setDesired(desired int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.desired != desired {
		s.addEventLocked("scale", fmt.Sprintf("desired runners %d → %d", s.desired, desired))
	}
	s.desired = desired
}

func (s *Supervisor) addEvent(kind, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.addEventLocked(kind, message)
}

func (s *Supervisor) addEventLocked(kind, message string) {
	s.logger.Info("supervisor event", "kind", kind, "message", message)

	event := Event{At: time.Now(), Kind: kind, Message: message}
	s.events = append([]Event{event}, s.events...)
	if len(s.events) > eventLimit {
		s.events = s.events[:eventLimit]
	}
}

// pruneDoneLocked keeps only the most recent finished runners.
func (s *Supervisor) pruneDoneLocked() {
	var done []*Runner
	for _, runner := range s.runners {
		if runner.State == StateDone {
			done = append(done, runner)
		}
	}
	if len(done) <= doneRunnerLimit {
		return
	}

	sort.Slice(done, func(i, j int) bool {
		return done[i].FinishedAt.Before(done[j].FinishedAt)
	})
	for _, runner := range done[:len(done)-doneRunnerLimit] {
		delete(s.runners, runner.Name)
	}
}

// Snapshot returns the current state of the supervisor.
func (s *Supervisor) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := Snapshot{
		ScaleSetName:       s.config.ScaleSetName,
		ScaleSetID:         s.config.ScaleSetID,
		MinRunners:         s.config.MinRunners,
		MaxRunners:         s.config.MaxRunners,
		DesiredRunners:     s.desired,
		ProvisionedRunners: s.provisioned,
		RetiredRunners:     s.retired,
		Polls:              s.polls,
		StartedAt:          s.startedAt,
		LastPollAt:         s.lastPollAt,
		Events:             append([]Event{}, s.events...),
		Runners:            make([]Runner, 0, len(s.runners)),
	}

	if s.statistics != nil {
		copied := *s.statistics
		snapshot.Statistics = &copied
	}

	for _, runner := range s.runners {
		snapshot.Runners = append(snapshot.Runners, *runner)

		switch runner.State {
		case StateBusy:
			snapshot.BusyRunners++
			snapshot.ActiveRunners++
		case StatePending:
			snapshot.PendingRunners++
			snapshot.ActiveRunners++
		}
	}

	sort.Slice(snapshot.Runners, func(i, j int) bool {
		left, right := snapshot.Runners[i], snapshot.Runners[j]
		if left.State != right.State {
			return stateRank(left.State) < stateRank(right.State)
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})

	return snapshot
}

func stateRank(state RunnerState) int {
	switch state {
	case StateBusy:
		return 0
	case StatePending:
		return 1
	default:
		return 2
	}
}
