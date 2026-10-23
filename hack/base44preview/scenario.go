package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/actions/scaleset"
)

// scenarioResult is the model the preview page renders.
type scenarioResult struct {
	ConfigURL string
	StartedAt string
	Total     string
	Failed    bool
	Steps     []scenarioStep
}

type scenarioStep struct {
	Title    string
	Status   string
	Summary  string
	Error    string
	Payload  string
	Duration string
}

// scenario collects the steps of one demo run.
type scenario struct {
	steps  []scenarioStep
	failed bool
}

// run executes one step. Once a step has failed, the remaining steps are
// recorded as skipped so the page always shows the complete flow.
func (s *scenario) run(title string, fn func() (string, any, error)) {
	if s.failed {
		s.steps = append(s.steps, scenarioStep{Title: title, Status: "skipped"})
		return
	}

	started := time.Now()
	summary, payload, err := fn()

	step := scenarioStep{
		Title:    title,
		Status:   "ok",
		Summary:  summary,
		Duration: time.Since(started).Round(time.Millisecond).String(),
		Payload:  formatPayload(payload),
	}
	if err != nil {
		step.Status = "failed"
		step.Error = err.Error()
		s.failed = true
	}

	s.steps = append(s.steps, step)
}

func formatPayload(payload any) string {
	if payload == nil {
		return ""
	}

	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Sprintf("failed to encode payload: %v", err)
	}

	return string(encoded)
}

// runScenario drives the scaleset client through a complete scale set flow
// against the stand-in GitHub API.
func runScenario(ctx context.Context, configURL string) scenarioResult {
	started := time.Now()
	s := &scenario{}

	var (
		client   *scaleset.Client
		scaleSet *scaleset.RunnerScaleSet
		session  *scaleset.MessageSessionClient
		message  *scaleset.RunnerScaleSetMessage
	)

	s.run("Create the client", func() (string, any, error) {
		c, err := scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{
			GitHubConfigURL:     configURL,
			PersonalAccessToken: "stub-personal-access-token",
			SystemInfo: scaleset.SystemInfo{
				System:    "base44-preview",
				Subsystem: "preview",
			},
		})
		if err != nil {
			return "", nil, err
		}

		client = c

		return fmt.Sprintf("client created for %s", configURL), json.RawMessage(c.DebugInfo()), nil
	})

	s.run("Authenticate and resolve the Actions tenant", func() (string, any, error) {
		// Any Actions service call first exchanges the registration token for
		// the tenant URL and admin token, which this call surfaces.
		scaleSets, err := client.ListRunnerScaleSets(ctx, 1)
		if err != nil {
			return "", nil, err
		}

		return fmt.Sprintf("registration token exchanged; %d scale set(s) already in runner group 1", len(scaleSets)),
			map[string]any{"runnerGroupId": 1, "scaleSets": scaleSets}, nil
	})

	s.run("Create the runner scale set", func() (string, any, error) {
		created, err := client.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
			Name:          "base44-preview",
			RunnerGroupID: 1,
			RunnerSetting: scaleset.RunnerSetting{DisableUpdate: true},
		})
		if err != nil {
			return "", nil, err
		}

		scaleSet = created

		return fmt.Sprintf("scale set %q created with id %d", created.Name, created.ID), created, nil
	})

	s.run("Open a message session", func() (string, any, error) {
		opened, err := client.MessageSessionClient(ctx, scaleSet.ID, "base44-preview")
		if err != nil {
			return "", nil, err
		}

		session = opened
		openedSession := session.Session()

		return fmt.Sprintf("session %s open against %s", openedSession.SessionID, openedSession.MessageQueueURL),
			openedSession, nil
	})

	s.run("Poll the message queue", func() (string, any, error) {
		polled, err := session.GetMessage(ctx, 0, 5)
		if err != nil {
			return "", nil, err
		}
		if polled == nil {
			return "", nil, errors.New("expected the stub to have a queued job, but the queue was empty")
		}

		message = polled

		return fmt.Sprintf(
				"message %d: %d job(s) waiting to be acquired, %d assigned job(s) in total",
				polled.MessageID, len(polled.JobAvailableMessages), polled.Statistics.TotalAssignedJobs,
			),
			map[string]any{
				"messageId":            polled.MessageID,
				"statistics":           polled.Statistics,
				"jobAvailableMessages": polled.JobAvailableMessages,
			}, nil
	})

	s.run("Acquire the queued job", func() (string, any, error) {
		if len(message.JobAvailableMessages) == 0 {
			return "", nil, errors.New("no JobAvailable message to acquire")
		}

		requestIDs := make([]int64, 0, len(message.JobAvailableMessages))
		for _, available := range message.JobAvailableMessages {
			requestIDs = append(requestIDs, available.RunnerRequestID)
		}

		acquired, err := session.AcquireJobs(ctx, requestIDs)
		if err != nil {
			return "", nil, err
		}

		return fmt.Sprintf("%d of %d request(s) acquired", len(acquired), len(requestIDs)),
			map[string]any{"requested": requestIDs, "acquired": acquired}, nil
	})

	s.run("Generate a JIT runner config", func() (string, any, error) {
		jitConfig, err := client.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{
			Name:       "base44-preview-runner-1",
			WorkFolder: "_work",
		}, scaleSet.ID)
		if err != nil {
			return "", nil, err
		}

		return fmt.Sprintf("runner %q registered (%d bytes of encoded JIT config)",
			jitConfig.Runner.Name, len(jitConfig.EncodedJITConfig),
		), jitConfig, nil
	})

	s.run("Acknowledge the message", func() (string, any, error) {
		if err := session.DeleteMessage(ctx, message.MessageID); err != nil {
			return "", nil, err
		}

		return fmt.Sprintf("message %d deleted from the queue", message.MessageID), nil, nil
	})

	s.run("Poll again once the queue is drained", func() (string, any, error) {
		polled, err := session.GetMessage(ctx, message.MessageID, 5)
		if err != nil {
			return "", nil, err
		}
		if polled != nil {
			return "", nil, fmt.Errorf("expected an empty queue, but message %d was returned", polled.MessageID)
		}

		return "no messages pending — the API answered 202 Accepted", nil, nil
	})

	s.run("Clean up", func() (string, any, error) {
		if err := session.Close(ctx); err != nil {
			return "", nil, err
		}
		if err := client.DeleteRunnerScaleSet(ctx, scaleSet.ID); err != nil {
			return "", nil, err
		}

		return "message session closed and scale set deleted", nil, nil
	})

	return scenarioResult{
		ConfigURL: configURL,
		StartedAt: started.UTC().Format(time.RFC3339),
		Total:     time.Since(started).Round(time.Millisecond).String(),
		Failed:    s.failed,
		Steps:     s.steps,
	}
}
