// Command supervisor runs a GitHub Actions runner scale set and serves a live
// view of what it is doing.
//
// It is the runnable companion to the supervisor package: it creates (or
// reuses) a runner scale set, opens a message session, and then polls for
// messages, acquires jobs, provisions runners with JIT configs and retires
// runners that are no longer needed. Open the address it prints to watch the
// scale set converge.
//
//	go run ./examples/supervisor \
//	  --url https://github.com/my-org \
//	  --name my-scale-set \
//	  --token "$GITHUB_TOKEN" \
//	  --max-runners 4 \
//	  --addr :3000
//
// Use --runner-command to start a runner for each JIT config; it receives
// $RUNNER_NAME and $RUNNER_JIT_CONFIG in its environment.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"github.com/actions/scaleset/supervisor"
	"github.com/google/uuid"
)

type config struct {
	registrationURL string
	scaleSetName    string
	runnerGroup     string
	minRunners      int
	maxRunners      int
	addr            string
	runnerCommand   string
	logLevel        string

	appClientID       string
	appInstallationID int64
	appPrivateKeyFile string
	token             string
}

func main() {
	if err := run(); err != nil {
		slog.Error("supervisor stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := parseFlags()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLevel(cfg.logLevel)}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := newClient(cfg)
	if err != nil {
		return err
	}

	runnerGroup, err := client.GetRunnerGroupByName(ctx, cfg.runnerGroup)
	if err != nil {
		return fmt.Errorf("look up runner group %q: %w", cfg.runnerGroup, err)
	}

	scaleSet, err := ensureScaleSet(ctx, client, runnerGroup.ID, cfg.scaleSetName, logger)
	if err != nil {
		return err
	}

	owner := ownerName()
	sessionClient, err := client.MessageSessionClient(ctx, scaleSet.ID, owner)
	if err != nil {
		return fmt.Errorf("create message session: %w", err)
	}

	scaleSetSupervisor, err := supervisor.New(sessionClient, client, supervisor.Config{
		ScaleSetID:   scaleSet.ID,
		ScaleSetName: scaleSet.Name,
		MinRunners:   cfg.minRunners,
		MaxRunners:   cfg.maxRunners,
		Launch:       newLauncher(cfg.runnerCommand),
		Logger:       logger.WithGroup("supervisor"),
	})
	if err != nil {
		return err
	}

	messageListener, err := listener.New(sessionClient, listener.Config{
		ScaleSetID: scaleSet.ID,
		MaxRunners: cfg.maxRunners,
		Logger:     logger.WithGroup("listener"),
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.addr,
		Handler:           scaleSetSupervisor.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	listenerErr := make(chan error, 1)
	go func() { listenerErr <- messageListener.Run(runCtx, scaleSetSupervisor) }()

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("live view listening", "addr", cfg.addr, "scaleSet", scaleSet.Name, "scaleSetID", scaleSet.ID)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case err := <-listenerErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("listener stopped: %w", err)
		}
	case err := <-serverErr:
		return fmt.Errorf("live view stopped: %w", err)
	}

	cancel()

	shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopShutdown()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("shutting the live view down", "error", err)
	}
	if err := sessionClient.Close(shutdownCtx); err != nil {
		logger.Warn("deleting the message session", "error", err)
	}

	return nil
}

func parseFlags() config {
	cfg := config{}

	flags := flag.NewFlagSet("supervisor", flag.ExitOnError)
	flags.StringVar(&cfg.registrationURL, "url", "", "REQUIRED: URL where the scale set is registered (e.g. https://github.com/my-org)")
	flags.StringVar(&cfg.scaleSetName, "name", "", "REQUIRED: name of the runner scale set")
	flags.StringVar(&cfg.runnerGroup, "runner-group", scaleset.DefaultRunnerGroup, "name of the runner group the scale set belongs to")
	flags.IntVar(&cfg.minRunners, "min-runners", 0, "number of runners kept while there is no demand")
	flags.IntVar(&cfg.maxRunners, "max-runners", 4, "maximum number of runners, reported to GitHub as the capacity")
	flags.StringVar(&cfg.addr, "addr", ":3000", "address the live view listens on")
	flags.StringVar(&cfg.runnerCommand, "runner-command", "", "optional command that starts a runner; $RUNNER_NAME and $RUNNER_JIT_CONFIG are set")
	flags.StringVar(&cfg.logLevel, "log-level", "info", "logging level (debug, info, warn, error)")

	flags.StringVar(&cfg.appClientID, "app-client-id", "", "GitHub App client id")
	flags.Int64Var(&cfg.appInstallationID, "app-installation-id", 0, "GitHub App installation id")
	flags.StringVar(&cfg.appPrivateKeyFile, "app-private-key-file", "", "path to the GitHub App private key")
	flags.StringVar(&cfg.token, "token", "", "personal access token (can be used in place of a GitHub App, although not recommended)")

	_ = flags.Parse(os.Args[1:])

	return cfg
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func newClient(cfg config) (*scaleset.Client, error) {
	if cfg.registrationURL == "" {
		return nil, errors.New("--url is required")
	}
	if cfg.scaleSetName == "" {
		return nil, errors.New("--name is required")
	}

	switch {
	case cfg.token != "":
		return scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{
			GitHubConfigURL:     cfg.registrationURL,
			PersonalAccessToken: cfg.token,
		})

	case cfg.appClientID != "" && cfg.appInstallationID != 0 && cfg.appPrivateKeyFile != "":
		privateKey, err := os.ReadFile(cfg.appPrivateKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read GitHub App private key: %w", err)
		}

		return scaleset.NewClientWithGitHubApp(scaleset.ClientWithGitHubAppConfig{
			GitHubConfigURL: cfg.registrationURL,
			GitHubAppAuth: scaleset.GitHubAppAuth{
				ClientID:       cfg.appClientID,
				InstallationID: cfg.appInstallationID,
				PrivateKey:     string(privateKey),
			},
		})

	default:
		return nil, errors.New("provide --token, or --app-client-id, --app-installation-id and --app-private-key-file")
	}
}

// ensureScaleSet reuses the scale set with this name or creates it.
func ensureScaleSet(ctx context.Context, client *scaleset.Client, runnerGroupID int, name string, logger *slog.Logger) (*scaleset.RunnerScaleSet, error) {
	existing, err := client.ListRunnerScaleSets(ctx, runnerGroupID)
	if err != nil {
		return nil, fmt.Errorf("list runner scale sets: %w", err)
	}

	for i := range existing {
		if existing[i].Name == name {
			logger.Info("using existing runner scale set", "name", name, "id", existing[i].ID)
			return &existing[i], nil
		}
	}

	created, err := client.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
		Name:          name,
		RunnerGroupID: runnerGroupID,
		Labels:        []scaleset.Label{{Name: name, Type: "System"}},
	})
	if err != nil {
		return nil, fmt.Errorf("create runner scale set: %w", err)
	}

	logger.Info("created runner scale set", "name", name, "id", created.ID)
	return created, nil
}

// newLauncher starts the runner command for each JIT config.
func newLauncher(command string) supervisor.Launcher {
	if command == "" {
		return nil
	}

	return func(ctx context.Context, runner supervisor.Runner, jitConfig string) error {
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Env = append(os.Environ(),
			"RUNNER_NAME="+runner.Name,
			"RUNNER_JIT_CONFIG="+jitConfig,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		return cmd.Run()
	}
}

func ownerName() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return uuid.NewString()
	}
	return hostname
}
