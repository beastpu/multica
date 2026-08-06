package featureflags

import (
	"context"

	"github.com/multica-ai/multica/server/pkg/featureflag"
)

const (
	// ComposioMCPApps gates the Composio app management UI and — together with
	// the MUL-3963 permission_mode / invocation_targets access model it depends
	// on — the aligned Private / Public-to picker in the agent create flow.
	// The access model exists to gate Composio sharing, so the two ship on the
	// same switch.
	ComposioMCPApps = "composio_mcp_apps"
	// CloudRuntime grants the managed cloud runtime surface to explicitly
	// approved workspaces. Rules target workspace_id and default off.
	CloudRuntime = "cloud_runtime"
	// AgentBuilder controls writes of system builder agents. It stays disabled
	// through the schema-only rollout so an older server cannot expose them.
	AgentBuilder = "agents_agent_builder"
	// ResourceLabels controls the agent- and skill-scoped label namespaces.
	// Issue labels remain available while this release flag is off.
	ResourceLabels = "settings_resource_labels"
	// WorkflowsActivityEngine gates every native workflow write and the
	// corresponding frontend surface. Reads remain available for recovery.
	WorkflowsActivityEngine = "workflows_activity_engine"
	// OpsPauseWorkflowProgression is an operational kill switch. It pauses
	// materialization and reconciliation without hiding or deleting runtime
	// data. The diagnostic sweeper continues to report anomalies.
	OpsPauseWorkflowProgression = "ops_pause_workflow_progression"
	// agentBuilderCompat is no longer a release flag. Keep publishing the key
	// as enabled so installed desktop clients that still gate the AI creation
	// entry on this config decision receive the permanently enabled behavior.
	agentBuilderCompat = "agents_agent_builder"
	// agentSkillTogglesCompat is no longer a release flag. Keep publishing the
	// key as enabled so installed v0.4.0 desktop clients, which still gate the
	// switch on this config decision, receive the permanently enabled behavior.
	agentSkillTogglesCompat = "agents_skill_toggles"
)

var frontendPublicFlags = []string{
	ComposioMCPApps,
	ResourceLabels,
	WorkflowsActivityEngine,
}

func ComposioMCPAppsEnabled(ctx context.Context, flags *featureflag.Service) bool {
	return flags.IsEnabled(ctx, ComposioMCPApps, false)
}

func CloudRuntimeEnabledForWorkspace(ctx context.Context, flags *featureflag.Service, workspaceID string) bool {
	eval := featureflag.EvalContextFrom(ctx)
	eval.WorkspaceID = workspaceID
	return flags.IsEnabled(featureflag.WithEvalContext(ctx, eval), CloudRuntime, false)
}

func AgentBuilderEnabled(ctx context.Context, flags *featureflag.Service) bool {
	return flags.IsEnabled(ctx, AgentBuilder, false)
}

func ResourceLabelsEnabled(ctx context.Context, flags *featureflag.Service) bool {
	return flags.IsEnabled(ctx, ResourceLabels, false)
}

func WorkflowsActivityEngineEnabled(ctx context.Context, flags *featureflag.Service) bool {
	return flags.IsEnabled(ctx, WorkflowsActivityEngine, false)
}

func WorkflowsActivityEngineEnabledForWorkspace(
	ctx context.Context,
	flags *featureflag.Service,
	workspaceID string,
) bool {
	eval := featureflag.EvalContextFrom(ctx)
	eval.WorkspaceID = workspaceID
	return flags.IsEnabled(
		featureflag.WithEvalContext(ctx, eval),
		WorkflowsActivityEngine,
		false,
	)
}

func WorkflowProgressionPaused(
	ctx context.Context,
	flags *featureflag.Service,
	workspaceID string,
) bool {
	eval := featureflag.EvalContextFrom(ctx)
	eval.WorkspaceID = workspaceID
	return flags.IsEnabled(
		featureflag.WithEvalContext(ctx, eval),
		OpsPauseWorkflowProgression,
		false,
	)
}

func EvaluateFrontendPublicFlags(ctx context.Context, flags *featureflag.Service) map[string]bool {
	out := make(map[string]bool, len(frontendPublicFlags)+2)
	for _, key := range frontendPublicFlags {
		out[key] = flags.IsEnabled(ctx, key, false)
	}
	out[agentBuilderCompat] = true
	out[agentSkillTogglesCompat] = true
	return out
}
