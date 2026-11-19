package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSession struct {
	acquired [][]int64
	err      error
}

func (f *fakeSession) AcquireJobs(_ context.Context, requestIDs []int64) ([]int64, error) {
	f.acquired = append(f.acquired, requestIDs)
	if f.err != nil {
		return nil, f.err
	}
	return requestIDs, nil
}

type fakeProvisioner struct {
	nextID    int
	names     []string
	removed   []int64
	removeErr error
}

func (f *fakeProvisioner) GenerateJitRunnerConfig(_ context.Context, setting *scaleset.RunnerScaleSetJitRunnerSetting, scaleSetID int) (*scaleset.RunnerScaleSetJitRunnerConfig, error) {
	f.nextID++
	f.names = append(f.names, setting.Name)

	return &scaleset.RunnerScaleSetJitRunnerConfig{
		Runner: &scaleset.RunnerReference{
			ID:               f.nextID,
			Name:             setting.Name,
			RunnerScaleSetID: scaleSetID,
		},
		EncodedJITConfig: "jit-config",
	}, nil
}

func (f *fakeProvisioner) RemoveRunner(_ context.Context, runnerID int64) error {
	f.removed = append(f.removed, runnerID)
	return f.removeErr
}

func newTestSupervisor(t *testing.T, config Config) (*Supervisor, *fakeSession, *fakeProvisioner) {
	t.Helper()

	if config.ScaleSetID == 0 {
		config.ScaleSetID = 42
	}

	session := &fakeSession{}
	provisioner := &fakeProvisioner{}

	created, err := New(session, provisioner, config)
	require.NoError(t, err)

	return created, session, provisioner
}

func statisticsWithAssignedJobs(count int) *scaleset.RunnerScaleSetStatistic {
	return &scaleset.RunnerScaleSetStatistic{TotalAssignedJobs: count}
}

func TestSupervisorProvisionsRunnersForAssignedJobs(t *testing.T) {
	sup, _, provisioner := newTestSupervisor(t, Config{MaxRunners: 4})

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		Statistics: statisticsWithAssignedJobs(2),
	}))

	require.Len(t, provisioner.names, 2)
	assert.Contains(t, provisioner.names[0], DefaultRunnerNamePrefix+"-")
	assert.NotEqual(t, provisioner.names[0], provisioner.names[1])

	snapshot := sup.Snapshot()
	assert.Equal(t, 2, snapshot.DesiredRunners)
	assert.Equal(t, 2, snapshot.ActiveRunners)
	assert.Equal(t, 2, snapshot.PendingRunners)
	assert.Equal(t, 0, snapshot.BusyRunners)
}

func TestSupervisorRespectsCapacityAndMinimum(t *testing.T) {
	sup, _, provisioner := newTestSupervisor(t, Config{MinRunners: 1, MaxRunners: 3})

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		Statistics: statisticsWithAssignedJobs(5),
	}))

	assert.Len(t, provisioner.names, 3)
	assert.Equal(t, 3, sup.Snapshot().DesiredRunners)
}

func TestSupervisorKeepsMinimumRunnersWhenIdle(t *testing.T) {
	sup, _, provisioner := newTestSupervisor(t, Config{MinRunners: 2, MaxRunners: 4})

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		Statistics: statisticsWithAssignedJobs(0),
	}))

	assert.Len(t, provisioner.names, 2)
}

func TestSupervisorAcquiresAvailableJobs(t *testing.T) {
	sup, session, _ := newTestSupervisor(t, Config{MaxRunners: 4})

	message := &scaleset.RunnerScaleSetMessage{
		Statistics: statisticsWithAssignedJobs(2),
		JobAvailableMessages: []*scaleset.JobAvailable{
			{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 11}},
			{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 22}},
		},
	}

	require.NoError(t, sup.Scale(context.Background(), message))
	require.Len(t, session.acquired, 1)
	assert.Equal(t, []int64{11, 22}, session.acquired[0])
}

func TestSupervisorReportsAcquireFailure(t *testing.T) {
	sup, session, _ := newTestSupervisor(t, Config{MaxRunners: 4})
	session.err = errors.New("acquire failed")

	err := sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		JobAvailableMessages: []*scaleset.JobAvailable{
			{JobMessageBase: scaleset.JobMessageBase{RunnerRequestID: 11}},
		},
	})

	require.ErrorContains(t, err, "acquire failed")
}

func TestSupervisorTracksJobLifecycle(t *testing.T) {
	sup, _, _ := newTestSupervisor(t, Config{MaxRunners: 4})

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		Statistics: statisticsWithAssignedJobs(1),
	}))
	require.Len(t, sup.Snapshot().Runners, 1)
	runner := sup.Snapshot().Runners[0]

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		JobStartedMessages: []*scaleset.JobStarted{{
			RunnerName:     runner.Name,
			RunnerID:       runner.ID,
			JobMessageBase: scaleset.JobMessageBase{JobID: "job-7", JobDisplayName: "build"},
		}},
	}))

	busy := sup.Snapshot()
	assert.Equal(t, 1, busy.BusyRunners)
	assert.Equal(t, 0, busy.PendingRunners)
	require.Len(t, busy.Runners, 1)
	assert.Equal(t, StateBusy, busy.Runners[0].State)
	assert.Equal(t, "job-7", busy.Runners[0].JobID)

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		JobCompletedMessages: []*scaleset.JobCompleted{{
			RunnerName:     runner.Name,
			RunnerID:       runner.ID,
			Result:         "success",
			JobMessageBase: scaleset.JobMessageBase{JobID: "job-7"},
		}},
		Statistics: statisticsWithAssignedJobs(0),
	}))

	done := sup.Snapshot()
	assert.Equal(t, StateDone, done.Runners[0].State)
	assert.Equal(t, 0, done.ActiveRunners)
	assert.Equal(t, 0, done.BusyRunners)
}

func TestSupervisorTracksRunnerItNoLongerTracks(t *testing.T) {
	sup, _, _ := newTestSupervisor(t, Config{MaxRunners: 4})

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		JobStartedMessages: []*scaleset.JobStarted{{
			RunnerName:     "retired-runner",
			RunnerID:       99,
			JobMessageBase: scaleset.JobMessageBase{JobID: "job-3"},
		}},
	}))

	snapshot := sup.Snapshot()
	assert.Equal(t, 1, snapshot.BusyRunners)
	require.Len(t, snapshot.Runners, 1)
	assert.Equal(t, "retired-runner", snapshot.Runners[0].Name)
	assert.Equal(t, 99, snapshot.Runners[0].ID)
}

func TestSupervisorRetiresPendingRunnersWhenDemandDrops(t *testing.T) {
	sup, _, provisioner := newTestSupervisor(t, Config{MaxRunners: 4})

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		Statistics: statisticsWithAssignedJobs(3),
	}))
	require.Len(t, provisioner.names, 3)

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		Statistics: statisticsWithAssignedJobs(1),
	}))

	// The two oldest runners are the ones retired.
	assert.Equal(t, []int64{1, 2}, provisioner.removed)

	snapshot := sup.Snapshot()
	assert.Equal(t, 1, snapshot.ActiveRunners)
	assert.Equal(t, 1, snapshot.DesiredRunners)
	assert.Equal(t, 2, snapshot.RetiredRunners)
}

func TestSupervisorDropsRunnerWhenRemovalFails(t *testing.T) {
	sup, _, provisioner := newTestSupervisor(t, Config{MaxRunners: 4})
	provisioner.removeErr = errors.New("runner not found")

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		Statistics: statisticsWithAssignedJobs(2),
	}))
	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		Statistics: statisticsWithAssignedJobs(0),
	}))

	// The runner is gone as far as GitHub is concerned, so the local view
	// must not keep it as capacity.
	assert.Equal(t, 0, sup.Snapshot().ActiveRunners)
}

func TestSupervisorDoesNotRetireBusyRunners(t *testing.T) {
	sup, _, provisioner := newTestSupervisor(t, Config{MaxRunners: 4})

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		Statistics: statisticsWithAssignedJobs(1),
	}))
	require.Len(t, sup.Snapshot().Runners, 1)
	runner := sup.Snapshot().Runners[0]

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		JobStartedMessages: []*scaleset.JobStarted{{
			RunnerName:     runner.Name,
			RunnerID:       runner.ID,
			JobMessageBase: scaleset.JobMessageBase{JobID: "job-1"},
		}},
	}))
	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		Statistics: statisticsWithAssignedJobs(0),
	}))

	assert.Empty(t, provisioner.removed)
	assert.Equal(t, 1, sup.Snapshot().ActiveRunners)
}

func TestSupervisorIgnoresEmptyPoll(t *testing.T) {
	sup, _, provisioner := newTestSupervisor(t, Config{MaxRunners: 2})

	require.NoError(t, sup.Scale(context.Background(), nil))

	assert.Empty(t, provisioner.names)
	assert.Equal(t, 1, sup.Snapshot().Polls)
}

func TestSupervisorCallsLauncherWithJitConfig(t *testing.T) {
	launched := make(chan string, 1)

	sup, _, _ := newTestSupervisor(t, Config{
		MaxRunners: 1,
		Launch: func(_ context.Context, runner Runner, jitConfig string) error {
			launched <- runner.Name + ":" + jitConfig
			return nil
		},
	})

	require.NoError(t, sup.Scale(context.Background(), &scaleset.RunnerScaleSetMessage{
		Statistics: statisticsWithAssignedJobs(1),
	}))

	require.Len(t, sup.Snapshot().Runners, 1)
	assert.Equal(t, sup.Snapshot().Runners[0].Name+":jit-config", <-launched)
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	_, err := New(&fakeSession{}, &fakeProvisioner{}, Config{ScaleSetID: 0})
	require.Error(t, err)

	_, err = New(&fakeSession{}, &fakeProvisioner{}, Config{ScaleSetID: 1, MinRunners: 3, MaxRunners: 1})
	require.Error(t, err)
}

func TestHandlerServesSnapshot(t *testing.T) {
	sup, _, _ := newTestSupervisor(t, Config{ScaleSetName: "demo", MaxRunners: 2})

	request := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	response := httptest.NewRecorder()

	sup.Handler().ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)

	var snapshot Snapshot
	require.NoError(t, json.NewDecoder(response.Body).Decode(&snapshot))
	assert.Equal(t, "demo", snapshot.ScaleSetName)
	assert.Equal(t, 2, snapshot.MaxRunners)
}
