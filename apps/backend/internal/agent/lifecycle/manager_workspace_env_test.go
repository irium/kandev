package lifecycle

import (
	"context"
	"maps"
	"sync"
	"testing"

	"github.com/kandev/kandev/internal/agent/executor"
	settingsmodels "github.com/kandev/kandev/internal/agent/settings/models"
	agentctl "github.com/kandev/kandev/internal/agentctl/client"
)

// envProfileResolver returns a fixed AgentProfileInfo with the supplied
// EnvVars so tests can drive what AgentProfile.EnvVars looks like.
type envProfileResolver struct {
	envVars []settingsmodels.EnvVar
}

func (r *envProfileResolver) ResolveProfile(_ context.Context, profileID string) (*AgentProfileInfo, error) {
	return &AgentProfileInfo{
		ProfileID: profileID,
		AgentID:   "auggie",
		AgentName: "auggie",
		EnvVars:   r.envVars,
	}, nil
}

// envCapturingExecutor records the ExecutorCreateRequest passed to
// CreateInstance so tests can assert what env reached the runtime layer.
type envCapturingExecutor struct {
	MockExecutor
	client      *agentctl.Client
	mu          sync.Mutex
	capturedEnv map[string]string
}

func (e *envCapturingExecutor) CreateInstance(_ context.Context, req *ExecutorCreateRequest) (*ExecutorInstance, error) {
	e.mu.Lock()
	e.capturedEnv = req.Env
	e.mu.Unlock()
	return &ExecutorInstance{
		InstanceID:    req.InstanceID,
		TaskID:        req.TaskID,
		SessionID:     req.SessionID,
		RuntimeName:   string(e.Name()),
		WorkspacePath: req.WorkspacePath,
		Client:        e.client,
	}, nil
}

func (e *envCapturingExecutor) capturedSnapshot() map[string]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]string, len(e.capturedEnv))
	maps.Copy(out, e.capturedEnv)
	return out
}

// TestCreateExecution_ForwardsAgentProfileEnv is the regression guard for the
// lazy workspace-only recovery path that fed an empty env into the runtime
// instance after backend restart. Without the fix, the ACP agent subprocess
// inherited an env without CLAUDE_CONFIG_DIR and session/load looked under
// the user-home default instead of the workspace's configured root, yielding
// -32002 Resource not found on resume.
func TestCreateExecution_ForwardsAgentProfileEnv(t *testing.T) {
	t.Parallel()
	log := newTestLogger()
	registry := newTestRegistry()
	execReg := NewExecutorRegistry(log)
	backend := &envCapturingExecutor{
		MockExecutor: MockExecutor{name: executor.NameStandalone},
		client:       newReadyAgentctlClient(t, log),
	}
	execReg.Register(backend)

	resolver := &envProfileResolver{envVars: []settingsmodels.EnvVar{
		{Key: "CLAUDE_CONFIG_DIR", Value: `/opt/workspace-cfg/.claude/`},
		{Key: "WORKSPACE_ROOT", Value: `/opt/workspace-cfg`},
	}}
	provider := &mockWorkspaceInfoProvider{
		envInfos: map[string]*WorkspaceInfo{
			"env-1": {
				TaskID:            "task-1",
				SessionID:         "session-1",
				TaskEnvironmentID: "env-1",
				WorkspacePath:     "/workspace/task-1",
				AgentID:           "auggie",
				AgentProfileID:    "profile-with-env",
			},
		},
	}
	mgr := NewManager(
		registry, &MockEventBus{}, execReg, &MockCredentialsManager{}, resolver, nil,
		ExecutorFallbackWarn, "", log,
	)
	mgr.workspaceInfoProvider = provider
	t.Cleanup(func() { close(mgr.stopCh) })

	if _, err := mgr.GetOrEnsureExecutionForEnvironment(context.Background(), "env-1"); err != nil {
		t.Fatalf("GetOrEnsureExecutionForEnvironment: %v", err)
	}

	got := backend.capturedSnapshot()
	if got["CLAUDE_CONFIG_DIR"] != `/opt/workspace-cfg/.claude/` {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want %q", got["CLAUDE_CONFIG_DIR"], `/opt/workspace-cfg/.claude/`)
	}
	if got["WORKSPACE_ROOT"] != `/opt/workspace-cfg` {
		t.Errorf("WORKSPACE_ROOT = %q, want %q", got["WORKSPACE_ROOT"], `/opt/workspace-cfg`)
	}
}

// TestBuildPassthroughEnv_MergesAgentProfileEnv asserts that passthrough
// sessions (CLI-driven agents like claude-code) also pick up the workspace's
// AgentProfile.EnvVars. Pre-fix the function returned only KANDEV_* markers +
// runtime-required creds, dropping CLAUDE_CONFIG_DIR and similar.
func TestBuildPassthroughEnv_MergesAgentProfileEnv(t *testing.T) {
	t.Parallel()
	resolver := &envProfileResolver{envVars: []settingsmodels.EnvVar{
		{Key: "CLAUDE_CONFIG_DIR", Value: `/opt/workspace-cfg/.claude/`},
	}}
	mgr := &Manager{
		profileResolver: resolver,
		credsMgr:        &MockCredentialsManager{},
		logger:          newTestLogger(),
	}
	exec := &AgentExecution{
		TaskID:         "task-1",
		SessionID:      "session-1",
		AgentProfileID: "profile-with-env",
	}

	env := mgr.buildPassthroughEnv(context.Background(), exec, nil)

	if env["CLAUDE_CONFIG_DIR"] != `/opt/workspace-cfg/.claude/` {
		t.Errorf("CLAUDE_CONFIG_DIR not forwarded: got %q", env["CLAUDE_CONFIG_DIR"])
	}
	if env["KANDEV_TASK_ID"] != "task-1" {
		t.Errorf("KANDEV_TASK_ID overridden or missing: got %q", env["KANDEV_TASK_ID"])
	}
	if env["KANDEV_SESSION_ID"] != "session-1" {
		t.Errorf("KANDEV_SESSION_ID overridden or missing: got %q", env["KANDEV_SESSION_ID"])
	}
}

// TestBuildPassthroughEnv_KandevKeysWinOverProfile guards the merge order:
// AgentProfile.EnvVars must not be able to spoof KANDEV_* markers (the
// orchestrator relies on these being authoritative for recovery).
func TestBuildPassthroughEnv_KandevKeysWinOverProfile(t *testing.T) {
	t.Parallel()
	resolver := &envProfileResolver{envVars: []settingsmodels.EnvVar{
		{Key: "KANDEV_TASK_ID", Value: "spoofed-task"},
	}}
	mgr := &Manager{
		profileResolver: resolver,
		credsMgr:        &MockCredentialsManager{},
		logger:          newTestLogger(),
	}
	exec := &AgentExecution{TaskID: "real-task", SessionID: "real-session"}

	env := mgr.buildPassthroughEnv(context.Background(), exec, nil)

	if env["KANDEV_TASK_ID"] != "real-task" {
		t.Errorf("KANDEV_TASK_ID spoofed by profile: got %q", env["KANDEV_TASK_ID"])
	}
}

// TestResolveAgentProfileEnv_NoEnvVars makes sure the helper returns nil (not
// an empty map) when the profile has no env vars — so callers can treat nil
// as "no profile env" without surprise.
func TestResolveAgentProfileEnv_NoEnvVars(t *testing.T) {
	t.Parallel()
	mgr := &Manager{
		profileResolver: &envProfileResolver{envVars: nil},
		logger:          newTestLogger(),
	}
	if got := mgr.resolveAgentProfileEnv(context.Background(), "any-profile"); got != nil {
		t.Errorf("expected nil for empty profile env, got %#v", got)
	}
}

// TestResolveAgentProfileEnv_NilResolver guards against a panic when the
// manager has no profile resolver wired (some legacy / test code paths).
func TestResolveAgentProfileEnv_NilResolver(t *testing.T) {
	t.Parallel()
	mgr := &Manager{logger: newTestLogger()}
	if got := mgr.resolveAgentProfileEnv(context.Background(), "p"); got != nil {
		t.Errorf("expected nil with no resolver, got %#v", got)
	}
}

