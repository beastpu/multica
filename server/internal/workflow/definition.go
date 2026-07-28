package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
)

const DefinitionSchemaVersion = 1

const (
	maxDefinitionBytes  = 1 << 20
	maxNodes            = 100
	maxRoles            = 50
	maxTasks            = 1000
	maxEdges            = 2000
	maxSubmissionFields = 500
	maxProposedTasks    = 100
)

type Definition struct {
	SchemaVersion int                  `json:"schema_version"`
	Name          string               `json:"name"`
	AppliesTo     AppliesTo            `json:"applies_to"`
	Roles         []RoleDefinition     `json:"roles"`
	Nodes         []NodeDefinition     `json:"nodes"`
	Edges         []EdgeDefinition     `json:"edges"`
	Acceptance    AcceptanceDefinition `json:"acceptance"`
	Layout        json.RawMessage      `json:"layout,omitempty"`
}

type AppliesTo struct {
	Kind    string `json:"kind"`
	TypeKey string `json:"type_key,omitempty"`
}

type RoleDefinition struct {
	Key               string   `json:"key"`
	Name              string   `json:"name"`
	Required          bool     `json:"required"`
	AllowedActorTypes []string `json:"allowed_actor_types"`
	// DefaultActorType/DefaultActorID optionally pin a template-level default
	// assignee for the role. Starting an instance pre-fills the role with this
	// actor (source "fixed") unless the caller assigns someone else.
	DefaultActorType string `json:"default_actor_type,omitempty"`
	DefaultActorID   string `json:"default_actor_id,omitempty"`
}

type NodeDefinition struct {
	Key              string                `json:"key"`
	Kind             string                `json:"kind"`
	JoinMode         string                `json:"join_mode,omitempty"`
	ActivityMode     string                `json:"activity_mode,omitempty"`
	Name             string                `json:"name"`
	Description      string                `json:"description,omitempty"`
	Color            string                `json:"color,omitempty"`
	TimeoutMinutes   int                   `json:"timeout_minutes,omitempty"`
	OwnerRole        string                `json:"owner_role,omitempty"`
	ParticipantRoles []string              `json:"participant_roles,omitempty"`
	Executor         ExecutorDefinition    `json:"executor,omitempty"`
	IssuePolicy      string                `json:"issue_policy,omitempty"`
	IssueTemplates   []IssueTemplate       `json:"issue_templates,omitempty"`
	Artifacts        []ArtifactRequirement `json:"artifacts,omitempty"`
	SubmissionSchema *SubmissionSchema     `json:"submission_schema,omitempty"`
	Verdict          *VerdictDefinition    `json:"verdict,omitempty"`
	Completion       CompletionDefinition  `json:"completion,omitempty"`
	// OnEnter/OnComplete run controlled side effects when an activity
	// activates or completes. Only white-listed action kinds are allowed;
	// notifications and integrations stay in their own subsystems.
	OnEnter    []NodeActionDefinition `json:"on_enter,omitempty"`
	OnComplete []NodeActionDefinition `json:"on_complete,omitempty"`
}

type NodeActionDefinition struct {
	Kind   string `json:"kind"`
	Status string `json:"status,omitempty"`
}

type ExecutorDefinition struct {
	Strategies []ExecutorStrategy `json:"strategies,omitempty"`
}

type ExecutorStrategy struct {
	Kind       string `json:"kind"`
	Role       string `json:"role,omitempty"`
	Capability string `json:"capability,omitempty"`
	Node       string `json:"node,omitempty"`
	Field      string `json:"field,omitempty"`
	ActorType  string `json:"actor_type,omitempty"`
	ActorID    string `json:"actor_id,omitempty"`
	// Condition gates the strategy: when set, the strategy is only
	// considered while resolving an executor if the condition evaluates to
	// true against upstream submissions, verdicts, and host fields. It uses
	// the same structured condition DSL as gateway edges.
	Condition json.RawMessage `json:"condition,omitempty"`
}

// HasCondition reports whether a raw condition document carries a value.
func HasCondition(raw json.RawMessage) bool {
	return hasJSONValue(raw)
}

type IssueTemplate struct {
	Key           string `json:"key"`
	Title         string `json:"title"`
	Description   string `json:"description,omitempty"`
	AssigneeRole  string `json:"assignee_role,omitempty"`
	AssigneeType  string `json:"assignee_type,omitempty"`
	AssigneeID    string `json:"assignee_id,omitempty"`
	Required      bool   `json:"required"`
	InitialStatus string `json:"initial_status,omitempty"`
	Priority      string `json:"priority,omitempty"`
}

// ArtifactRequirement declares one formal output a node is expected to
// deliver. Kind selects the carrier: a document is stored inline, an
// attachment references the existing attachment entity, and a link holds an
// external address such as a pull request.
type ArtifactRequirement struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// ArtifactKind resolves a requirement's carrier, defaulting to document. The
// default matters because most node outputs are written prose, and a template
// author who omits the field means "a document".
func ArtifactKind(requirement ArtifactRequirement) string {
	if requirement.Kind == "" {
		return "document"
	}
	return requirement.Kind
}

// RequiredArtifacts returns the requirements that block node completion.
func RequiredArtifacts(node NodeDefinition) []ArtifactRequirement {
	required := make([]ArtifactRequirement, 0, len(node.Artifacts))
	for _, requirement := range node.Artifacts {
		if requirement.Required {
			required = append(required, requirement)
		}
	}
	return required
}

type SubmissionSchema struct {
	Policy string            `json:"policy,omitempty"`
	Fields []SubmissionField `json:"fields"`
}

type SubmissionField struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type VerdictDefinition struct {
	Evaluator      string          `json:"evaluator"`
	RequiredResult string          `json:"required_result,omitempty"`
	Condition      json.RawMessage `json:"condition,omitempty"`
}

type CompletionDefinition struct {
	Mode                 string `json:"mode,omitempty"`
	RequiredIssueOutcome string `json:"required_issue_outcome,omitempty"`
	SubmissionRequired   bool   `json:"submission_required,omitempty"`
	// HandoffRequired blocks completion until the node carries a non-empty
	// handoff summary. It is separate from SubmissionRequired because a node
	// can owe a structured result without owing a conclusion, and far more
	// often owes the conclusion alone.
	HandoffRequired bool   `json:"handoff_required,omitempty"`
	VerdictRequired string `json:"verdict_required,omitempty"`
	Confirmation    string `json:"confirmation,omitempty"`
	// AuthorizedRoles lists workflow roles whose resolved member actors may
	// force-complete, skip, or roll back this node in addition to the
	// defaults (workspace admins always; the node owner for manual
	// completion).
	AuthorizedRoles []string `json:"authorized_roles,omitempty"`
}

// MaxHandoffSummaryChars caps the handoff summary. The point of the summary is
// that a downstream node can read it in full without deciding whether to; a cap
// is what keeps that true as a workflow grows.
const MaxHandoffSummaryChars = 500

type EdgeDefinition struct {
	From      string          `json:"from"`
	To        string          `json:"to"`
	Condition json.RawMessage `json:"condition,omitempty"`
	Default   bool            `json:"default,omitempty"`
}

type AcceptanceDefinition struct {
	Policy        string   `json:"policy,omitempty"`
	ApproverRole  string   `json:"approver_role,omitempty"`
	NodeKey       string   `json:"node_key,omitempty"`
	ReworkTargets []string `json:"rework_targets,omitempty"`
}

func ParseDefinition(raw []byte) (Definition, error) {
	var definition Definition
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return Definition{}, fmt.Errorf("decode workflow definition: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Definition{}, err
	}
	if err := ValidateDefinition(definition); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("workflow definition contains trailing JSON")
		}
		return fmt.Errorf("decode trailing workflow definition: %w", err)
	}
	return nil
}

func ValidateDefinition(definition Definition) error {
	encoded, err := json.Marshal(definition)
	if err != nil {
		return fmt.Errorf("encode workflow definition: %w", err)
	}
	if len(encoded) > maxDefinitionBytes {
		return fmt.Errorf("workflow definition exceeds limit %d bytes", maxDefinitionBytes)
	}
	if definition.SchemaVersion != DefinitionSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", definition.SchemaVersion)
	}
	if strings.TrimSpace(definition.Name) == "" {
		return errors.New("name is required")
	}
	if definition.AppliesTo.Kind != "issue" {
		return errors.New("applies_to.kind must be issue")
	}
	if len(definition.Roles) > maxRoles {
		return fmt.Errorf("roles exceeds limit %d", maxRoles)
	}
	if len(definition.Nodes) == 0 || len(definition.Nodes) > maxNodes {
		return fmt.Errorf("nodes must contain between 1 and %d entries", maxNodes)
	}
	if len(definition.Edges) > maxEdges {
		return fmt.Errorf("edges exceeds limit %d", maxEdges)
	}

	roles, err := validateRoles(definition.Roles)
	if err != nil {
		return err
	}
	nodes, startKey, endCount, taskCount, err := validateNodes(definition.Nodes, roles)
	if err != nil {
		return err
	}
	if endCount == 0 {
		return errors.New("at least one end node is required")
	}
	if taskCount > maxTasks {
		return fmt.Errorf("issue templates exceeds limit %d", maxTasks)
	}
	if err := validateGraph(nodes, startKey, definition.Edges); err != nil {
		return err
	}
	if err := validateExecutorReferences(definition.Nodes, nodes, roles, definition.Edges); err != nil {
		return err
	}
	if err := validateAcceptance(definition.Acceptance, nodes, roles, definition.Edges); err != nil {
		return err
	}
	return nil
}

func validateRoles(definitions []RoleDefinition) (map[string]RoleDefinition, error) {
	roles := make(map[string]RoleDefinition, len(definitions))
	for _, role := range definitions {
		if !validKey(role.Key) {
			return nil, fmt.Errorf("invalid role key %q", role.Key)
		}
		if _, exists := roles[role.Key]; exists {
			return nil, fmt.Errorf("duplicate role key %q", role.Key)
		}
		if strings.TrimSpace(role.Name) == "" {
			return nil, fmt.Errorf("role %q name is required", role.Key)
		}
		if len(role.AllowedActorTypes) == 0 {
			return nil, fmt.Errorf("role %q allowed_actor_types is required", role.Key)
		}
		actorTypes := map[string]struct{}{}
		for _, actorType := range role.AllowedActorTypes {
			if actorType != "member" && actorType != "agent" && actorType != "squad" {
				return nil, fmt.Errorf("role %q has invalid actor type %q", role.Key, actorType)
			}
			if _, exists := actorTypes[actorType]; exists {
				return nil, fmt.Errorf("role %q has duplicate actor type %q", role.Key, actorType)
			}
			actorTypes[actorType] = struct{}{}
		}
		if err := validateDirectActor(role.DefaultActorType, role.DefaultActorID); err != nil {
			return nil, fmt.Errorf("role %q default actor: %w", role.Key, err)
		}
		if role.DefaultActorType != "" {
			if _, allowed := actorTypes[role.DefaultActorType]; !allowed {
				return nil, fmt.Errorf(
					"role %q default actor type %q is not in allowed_actor_types",
					role.Key,
					role.DefaultActorType,
				)
			}
		}
		roles[role.Key] = role
	}
	return roles, nil
}

func validateNodes(definitions []NodeDefinition, roles map[string]RoleDefinition) (map[string]NodeDefinition, string, int, int, error) {
	nodes := make(map[string]NodeDefinition, len(definitions))
	startKey := ""
	endCount := 0
	taskCount := 0
	submissionFieldCount := 0
	taskKeys := map[string]string{}
	for _, node := range definitions {
		if !validKey(node.Key) {
			return nil, "", 0, 0, fmt.Errorf("invalid node key %q", node.Key)
		}
		if _, exists := nodes[node.Key]; exists {
			return nil, "", 0, 0, fmt.Errorf("duplicate node key %q", node.Key)
		}
		if strings.TrimSpace(node.Name) == "" {
			return nil, "", 0, 0, fmt.Errorf("node %q name is required", node.Key)
		}
		// Only an activity has an executor to hold responsible for an output.
		// A control node declaring one would create a requirement nobody can
		// satisfy, and the node would never complete.
		if len(node.Artifacts) > 0 && node.Kind != "activity" {
			return nil, "", 0, 0, fmt.Errorf(
				"node %q cannot declare artifacts outside an activity", node.Key,
			)
		}
		switch node.Kind {
		case "start":
			if startKey != "" {
				return nil, "", 0, 0, errors.New("exactly one start node is required")
			}
			startKey = node.Key
		case "end":
			endCount++
		case "activity":
			if err := validateActivity(node, roles, taskKeys); err != nil {
				return nil, "", 0, 0, err
			}
		case "gateway", "parallel_split", "parallel_join", "wait":
		default:
			return nil, "", 0, 0, fmt.Errorf("node %q has invalid kind %q", node.Key, node.Kind)
		}
		if node.Kind != "activity" &&
			(len(node.OnEnter) > 0 || len(node.OnComplete) > 0) {
			return nil, "", 0, 0, fmt.Errorf(
				"node %q cannot declare actions; only activities run side effects",
				node.Key,
			)
		}
		taskCount += len(node.IssueTemplates)
		if node.SubmissionSchema != nil {
			submissionFieldCount += len(node.SubmissionSchema.Fields)
		}
		nodes[node.Key] = node
	}
	if startKey == "" {
		return nil, "", 0, 0, errors.New("exactly one start node is required")
	}
	if submissionFieldCount > maxSubmissionFields {
		return nil, "", 0, 0, fmt.Errorf(
			"submission fields exceeds limit %d",
			maxSubmissionFields,
		)
	}
	return nodes, startKey, endCount, taskCount, nil
}

func validateActivity(
	node NodeDefinition,
	roles map[string]RoleDefinition,
	versionTaskKeys map[string]string,
) error {
	if node.ActivityMode != "" && node.ActivityMode != "work" && node.ActivityMode != "acceptance" {
		return fmt.Errorf("activity %q has invalid activity_mode %q", node.Key, node.ActivityMode)
	}
	if node.TimeoutMinutes < 0 || node.TimeoutMinutes > 525600 {
		return fmt.Errorf("activity %q has invalid timeout_minutes", node.Key)
	}
	if node.OwnerRole != "" {
		if _, ok := roles[node.OwnerRole]; !ok {
			return fmt.Errorf("activity %q references unknown owner role %q", node.Key, node.OwnerRole)
		}
	}
	for _, role := range node.ParticipantRoles {
		if _, ok := roles[role]; !ok {
			return fmt.Errorf("activity %q references unknown participant role %q", node.Key, role)
		}
	}
	if err := validateExecutor(node, roles); err != nil {
		return err
	}
	if err := validateNodeActions(node.Key, "on_enter", node.OnEnter); err != nil {
		return err
	}
	if err := validateNodeActions(node.Key, "on_complete", node.OnComplete); err != nil {
		return err
	}
	switch node.IssuePolicy {
	case "", "none", "fixed", "dynamic", "fixed_and_dynamic":
	default:
		return fmt.Errorf("activity %q has invalid issue_policy %q", node.Key, node.IssuePolicy)
	}
	if node.IssuePolicy == "none" && len(node.IssueTemplates) > 0 {
		return fmt.Errorf("activity %q issue_policy none cannot declare issue templates", node.Key)
	}
	if node.IssuePolicy == "dynamic" && len(node.IssueTemplates) > 0 {
		return fmt.Errorf("activity %q issue_policy dynamic cannot declare fixed issue templates", node.Key)
	}
	taskKeys := map[string]struct{}{}
	for _, task := range node.IssueTemplates {
		if !validKey(task.Key) {
			return fmt.Errorf("activity %q has invalid task key %q", node.Key, task.Key)
		}
		if _, exists := taskKeys[task.Key]; exists {
			return fmt.Errorf("activity %q has duplicate task key %q", node.Key, task.Key)
		}
		taskKeys[task.Key] = struct{}{}
		if owner, exists := versionTaskKeys[task.Key]; exists {
			return fmt.Errorf(
				"activity %q task key %q is already used by activity %q",
				node.Key,
				task.Key,
				owner,
			)
		}
		versionTaskKeys[task.Key] = node.Key
		if strings.TrimSpace(task.Title) == "" {
			return fmt.Errorf("activity %q task %q title is required", node.Key, task.Key)
		}
		if err := validateIssueTitleTemplate(task.Title); err != nil {
			return fmt.Errorf("activity %q task %q: %w", node.Key, task.Key, err)
		}
		if task.AssigneeRole != "" {
			if _, ok := roles[task.AssigneeRole]; !ok {
				return fmt.Errorf("activity %q task %q references unknown assignee role %q", node.Key, task.Key, task.AssigneeRole)
			}
		}
		if task.AssigneeRole != "" &&
			(task.AssigneeType != "" || task.AssigneeID != "") {
			return fmt.Errorf(
				"activity %q task %q cannot declare both assignee_role and a direct assignee",
				node.Key,
				task.Key,
			)
		}
		if err := validateDirectActor(task.AssigneeType, task.AssigneeID); err != nil {
			return fmt.Errorf("activity %q task %q: %w", node.Key, task.Key, err)
		}
		switch task.InitialStatus {
		case "", "backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled":
		default:
			return fmt.Errorf("activity %q task %q has invalid initial_status %q", node.Key, task.Key, task.InitialStatus)
		}
		switch task.Priority {
		case "", "none", "low", "medium", "high", "urgent":
		default:
			return fmt.Errorf("activity %q task %q has invalid priority %q", node.Key, task.Key, task.Priority)
		}
	}
	artifactKeys := map[string]struct{}{}
	for _, requirement := range node.Artifacts {
		if !validKey(requirement.Key) {
			return fmt.Errorf("activity %q has invalid artifact key %q", node.Key, requirement.Key)
		}
		if _, exists := artifactKeys[requirement.Key]; exists {
			return fmt.Errorf("activity %q has duplicate artifact key %q", node.Key, requirement.Key)
		}
		artifactKeys[requirement.Key] = struct{}{}
		if strings.TrimSpace(requirement.Name) == "" {
			return fmt.Errorf("activity %q artifact %q name is required", node.Key, requirement.Key)
		}
		switch requirement.Kind {
		case "", "document", "attachment", "link":
		default:
			return fmt.Errorf(
				"activity %q artifact %q has invalid kind %q",
				node.Key, requirement.Key, requirement.Kind,
			)
		}
	}
	if node.SubmissionSchema != nil {
		policy := node.SubmissionSchema.Policy
		if policy == "" {
			policy = "single"
		}
		switch policy {
		case "single", "per_required_task", "fan_in":
		case "none":
			if len(node.SubmissionSchema.Fields) > 0 {
				return fmt.Errorf(
					"activity %q submission policy none cannot declare fields",
					node.Key,
				)
			}
		default:
			return fmt.Errorf(
				"activity %q has invalid submission policy %q",
				node.Key,
				node.SubmissionSchema.Policy,
			)
		}
		fieldKeys := map[string]struct{}{}
		for _, field := range node.SubmissionSchema.Fields {
			if !validKey(field.Key) {
				return fmt.Errorf("activity %q has invalid submission field key %q", node.Key, field.Key)
			}
			if _, exists := fieldKeys[field.Key]; exists {
				return fmt.Errorf("activity %q has duplicate submission field key %q", node.Key, field.Key)
			}
			fieldKeys[field.Key] = struct{}{}
			if strings.TrimSpace(field.Name) == "" {
				return fmt.Errorf(
					"activity %q submission field %q name is required",
					node.Key,
					field.Key,
				)
			}
			switch field.Type {
			case "text", "number", "boolean", "date", "member", "agent", "squad":
			default:
				return fmt.Errorf("activity %q field %q has invalid type %q", node.Key, field.Key, field.Type)
			}
		}
	}
	if node.Verdict != nil {
		if node.Verdict.Evaluator != "deterministic" && node.Verdict.Evaluator != "member" {
			return fmt.Errorf("activity %q has invalid verdict evaluator %q", node.Key, node.Verdict.Evaluator)
		}
		if node.Verdict.RequiredResult != "" && node.Verdict.RequiredResult != "pass" &&
			node.Verdict.RequiredResult != "not_blocked" {
			return fmt.Errorf("activity %q has invalid verdict required_result %q", node.Key, node.Verdict.RequiredResult)
		}
		if node.Verdict.Evaluator != "deterministic" &&
			hasJSONValue(node.Verdict.Condition) {
			return fmt.Errorf(
				"activity %q only a deterministic verdict can declare condition",
				node.Key,
			)
		}
	}
	switch node.Completion.RequiredIssueOutcome {
	case "", "done", "terminal", "none":
	default:
		return fmt.Errorf(
			"activity %q has invalid required_issue_outcome %q",
			node.Key,
			node.Completion.RequiredIssueOutcome,
		)
	}
	switch node.Completion.Mode {
	case "", "automatic", "manual":
	default:
		return fmt.Errorf(
			"activity %q has invalid completion mode %q",
			node.Key,
			node.Completion.Mode,
		)
	}
	switch node.Completion.VerdictRequired {
	case "", "none":
	case "pass", "not_blocked":
		if node.Verdict == nil {
			return fmt.Errorf("activity %q completion requires a verdict definition", node.Key)
		}
	default:
		return fmt.Errorf(
			"activity %q has invalid verdict_required %q",
			node.Key,
			node.Completion.VerdictRequired,
		)
	}
	for _, roleKey := range node.Completion.AuthorizedRoles {
		if _, ok := roles[roleKey]; !ok {
			return fmt.Errorf(
				"activity %q completion references unknown authorized role %q",
				node.Key,
				roleKey,
			)
		}
	}
	switch node.Completion.Confirmation {
	case "", "none", "member_any", "member_all", "admin_only":
	case "owner_any", "owner_all":
		// Owners come from node participants at runtime, so either a
		// member-only owner role or a directly pinned member owner can carry
		// the confirmation.
		if node.OwnerRole == "" {
			if !PinsMemberOwner(node) {
				return fmt.Errorf(
					"activity %q owner confirmation requires an owner role or a pinned member owner",
					node.Key,
				)
			}
			break
		}
		role, ok := roles[node.OwnerRole]
		if !ok {
			return fmt.Errorf("activity %q owner confirmation requires owner_role", node.Key)
		}
		if !roleResolvesOnlyToMember(role) {
			return fmt.Errorf(
				"activity %q owner confirmation role must resolve only to member",
				node.Key,
			)
		}
	default:
		return fmt.Errorf(
			"activity %q has invalid confirmation %q",
			node.Key,
			node.Completion.Confirmation,
		)
	}
	return nil
}

const maxNodeActions = 8

func validateNodeActions(nodeKey, phase string, actions []NodeActionDefinition) error {
	if len(actions) > maxNodeActions {
		return fmt.Errorf(
			"activity %q %s actions exceed limit %d",
			nodeKey,
			phase,
			maxNodeActions,
		)
	}
	for _, action := range actions {
		switch action.Kind {
		case "set_host_status":
			switch action.Status {
			case "backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled":
			default:
				return fmt.Errorf(
					"activity %q %s action has invalid status %q",
					nodeKey,
					phase,
					action.Status,
				)
			}
		default:
			return fmt.Errorf(
				"activity %q %s has invalid action kind %q",
				nodeKey,
				phase,
				action.Kind,
			)
		}
	}
	return nil
}

func AllowsDynamicIssues(node NodeDefinition) bool {
	return node.IssuePolicy == "dynamic" || node.IssuePolicy == "fixed_and_dynamic"
}

// NormalizeProposedTasks validates the stable task contract carried by a
// Submission before a member confirms the fan-out. Role references are
// validated against the running instance by the handler because a node
// snapshot intentionally does not duplicate the template's role definitions.
func NormalizeProposedTasks(tasks []IssueTemplate) ([]IssueTemplate, error) {
	if len(tasks) > maxProposedTasks {
		return nil, fmt.Errorf("proposed_tasks exceeds limit %d", maxProposedTasks)
	}
	normalized := make([]IssueTemplate, len(tasks))
	keys := make(map[string]struct{}, len(tasks))
	for index, task := range tasks {
		task.Key = strings.TrimSpace(task.Key)
		task.Title = strings.TrimSpace(task.Title)
		task.Description = strings.TrimSpace(task.Description)
		task.AssigneeRole = strings.TrimSpace(task.AssigneeRole)
		task.AssigneeType = strings.TrimSpace(task.AssigneeType)
		task.AssigneeID = strings.TrimSpace(task.AssigneeID)
		if !validKey(task.Key) {
			return nil, fmt.Errorf("proposed task has invalid key %q", task.Key)
		}
		if _, exists := keys[task.Key]; exists {
			return nil, fmt.Errorf("proposed_tasks has duplicate key %q", task.Key)
		}
		keys[task.Key] = struct{}{}
		if task.Title == "" {
			return nil, fmt.Errorf("proposed task %q title is required", task.Key)
		}
		if err := validateIssueTitleTemplate(task.Title); err != nil {
			return nil, fmt.Errorf("proposed task %q: %w", task.Key, err)
		}
		if task.AssigneeRole != "" &&
			(task.AssigneeType != "" || task.AssigneeID != "") {
			return nil, fmt.Errorf(
				"proposed task %q cannot declare both assignee_role and a direct assignee",
				task.Key,
			)
		}
		if err := validateDirectActor(task.AssigneeType, task.AssigneeID); err != nil {
			return nil, fmt.Errorf("proposed task %q: %w", task.Key, err)
		}
		if task.InitialStatus == "" {
			task.InitialStatus = "todo"
		}
		switch task.InitialStatus {
		case "backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled":
		default:
			return nil, fmt.Errorf(
				"proposed task %q has invalid initial_status %q",
				task.Key,
				task.InitialStatus,
			)
		}
		if task.Priority == "" {
			task.Priority = "none"
		}
		switch task.Priority {
		case "none", "low", "medium", "high", "urgent":
		default:
			return nil, fmt.Errorf(
				"proposed task %q has invalid priority %q",
				task.Key,
				task.Priority,
			)
		}
		normalized[index] = task
	}
	return normalized, nil
}

func SubmissionPolicy(node NodeDefinition) string {
	if node.SubmissionSchema == nil {
		return "none"
	}
	if node.SubmissionSchema.Policy == "" {
		return "single"
	}
	return node.SubmissionSchema.Policy
}

func RequiresManualCompletion(node NodeDefinition) bool {
	if node.Kind != "activity" || node.ActivityMode == "acceptance" {
		return false
	}
	switch node.Completion.Mode {
	case "manual":
		return true
	case "automatic":
		return false
	}
	if node.SubmissionSchema != nil || node.Completion.SubmissionRequired ||
		node.Verdict != nil {
		return false
	}
	if node.Completion.VerdictRequired != "" &&
		node.Completion.VerdictRequired != "none" {
		return false
	}
	if node.Completion.Confirmation != "" &&
		node.Completion.Confirmation != "none" {
		return false
	}
	for _, task := range node.IssueTemplates {
		if task.Required {
			return false
		}
	}
	return true
}

func validateExecutor(node NodeDefinition, roles map[string]RoleDefinition) error {
	if len(node.Executor.Strategies) == 0 {
		return nil
	}
	hasFallback := false
	for _, strategy := range node.Executor.Strategies {
		switch strategy.Kind {
		case "fixed_actor":
			if err := validateDirectActor(strategy.ActorType, strategy.ActorID); err != nil {
				return fmt.Errorf("activity %q executor: %w", node.Key, err)
			}
		case "fixed_role", "fallback_role":
			if _, ok := roles[strategy.Role]; !ok {
				return fmt.Errorf("activity %q executor references unknown role %q", node.Key, strategy.Role)
			}
			if strategy.Kind == "fallback_role" && !hasJSONValue(strategy.Condition) {
				hasFallback = true
			}
		case "manual":
			if !hasJSONValue(strategy.Condition) {
				hasFallback = true
			}
		case "previous_selected":
			if !validKey(strategy.Node) || !validKey(strategy.Field) {
				return fmt.Errorf(
					"activity %q previous_selected requires node and field",
					node.Key,
				)
			}
		case "capability_match":
			if strings.TrimSpace(strategy.Capability) == "" {
				return fmt.Errorf("activity %q capability_match requires capability", node.Key)
			}
			role, ok := roles[strategy.Role]
			if !ok {
				return fmt.Errorf(
					"activity %q capability_match references unknown pool role %q",
					node.Key,
					strategy.Role,
				)
			}
			allowedPoolActor := false
			for _, actorType := range role.AllowedActorTypes {
				allowedPoolActor = allowedPoolActor || actorType == "agent" || actorType == "squad"
			}
			if !allowedPoolActor {
				return fmt.Errorf(
					"activity %q capability_match pool role must allow agent or squad",
					node.Key,
				)
			}
		default:
			return fmt.Errorf("activity %q has invalid executor strategy %q", node.Key, strategy.Kind)
		}
	}
	if !hasFallback {
		return fmt.Errorf(
			"activity %q executor requires an unconditional fallback_role or manual",
			node.Key,
		)
	}
	return nil
}

func validateDirectActor(actorType, actorID string) error {
	if actorType == "" && actorID == "" {
		return nil
	}
	if actorType == "" || actorID == "" {
		return errors.New("assignee_type and assignee_id must be declared together")
	}
	switch actorType {
	case "member", "agent", "squad":
	default:
		return fmt.Errorf("invalid actor type %q", actorType)
	}
	if _, err := uuid.Parse(actorID); err != nil {
		return fmt.Errorf("invalid actor id %q", actorID)
	}
	return nil
}

func validateExecutorReferences(
	nodeDefinitions []NodeDefinition,
	nodes map[string]NodeDefinition,
	_ map[string]RoleDefinition,
	edges []EdgeDefinition,
) error {
	for _, node := range nodeDefinitions {
		for _, strategy := range node.Executor.Strategies {
			if hasJSONValue(strategy.Condition) {
				if err := ValidateCondition(strategy.Condition, nodes); err != nil {
					return fmt.Errorf(
						"activity %q executor strategy condition: %w",
						node.Key,
						err,
					)
				}
				references, err := ConditionReferences(strategy.Condition)
				if err != nil {
					return fmt.Errorf(
						"activity %q executor strategy condition: %w",
						node.Key,
						err,
					)
				}
				for _, reference := range references {
					// Executor resolution runs when the node activates, so
					// the node's own submissions and verdicts do not exist
					// yet; conditions may only read upstream nodes.
					if reference.Node == node.Key ||
						!workflowPathExists(reference.Node, node.Key, edges) {
						return fmt.Errorf(
							"activity %q executor condition node %q must be upstream",
							node.Key,
							reference.Node,
						)
					}
				}
			}
			if strategy.Kind != "previous_selected" {
				continue
			}
			source, ok := nodes[strategy.Node]
			if !ok || source.Kind != "activity" || source.SubmissionSchema == nil {
				return fmt.Errorf(
					"activity %q previous_selected references unknown submission node %q",
					node.Key,
					strategy.Node,
				)
			}
			fieldType := ""
			for _, field := range source.SubmissionSchema.Fields {
				if field.Key == strategy.Field {
					fieldType = field.Type
					break
				}
			}
			switch fieldType {
			case "member", "agent", "squad":
			default:
				return fmt.Errorf(
					"activity %q previous_selected field %q must be member, agent, or squad",
					node.Key,
					strategy.Field,
				)
			}
			if !workflowPathExists(strategy.Node, node.Key, edges) {
				return fmt.Errorf(
					"activity %q previous_selected node %q must be upstream",
					node.Key,
					strategy.Node,
				)
			}
		}
		if node.Verdict == nil || !hasJSONValue(node.Verdict.Condition) {
			continue
		}
		if err := ValidateCondition(node.Verdict.Condition, nodes); err != nil {
			return fmt.Errorf(
				"activity %q deterministic verdict condition: %w",
				node.Key,
				err,
			)
		}
		references, err := ConditionReferences(node.Verdict.Condition)
		if err != nil {
			return fmt.Errorf(
				"activity %q deterministic verdict condition: %w",
				node.Key,
				err,
			)
		}
		for _, reference := range references {
			if reference.Node == node.Key {
				if reference.Source == "node_verdict" {
					return fmt.Errorf(
						"activity %q deterministic verdict cannot reference itself",
						node.Key,
					)
				}
				continue
			}
			if !workflowPathExists(reference.Node, node.Key, edges) {
				return fmt.Errorf(
					"activity %q deterministic verdict node %q must be upstream",
					node.Key,
					reference.Node,
				)
			}
		}
	}
	return nil
}

func validateGraph(nodes map[string]NodeDefinition, startKey string, edges []EdgeDefinition) error {
	outgoing := make(map[string][]EdgeDefinition, len(nodes))
	incoming := make(map[string][]EdgeDefinition, len(nodes))
	edgeKeys := map[string]struct{}{}
	for _, edge := range edges {
		if _, ok := nodes[edge.From]; !ok {
			return fmt.Errorf("edge references unknown source node %q", edge.From)
		}
		if _, ok := nodes[edge.To]; !ok {
			return fmt.Errorf("edge references unknown target node %q", edge.To)
		}
		key := edge.From + "\x00" + edge.To
		if _, exists := edgeKeys[key]; exists {
			return fmt.Errorf("duplicate edge %q -> %q", edge.From, edge.To)
		}
		edgeKeys[key] = struct{}{}
		outgoing[edge.From] = append(outgoing[edge.From], edge)
		incoming[edge.To] = append(incoming[edge.To], edge)
	}
	degrees := make(map[string]int, len(nodes))
	cycleQueue := make([]string, 0, len(nodes))
	for key := range nodes {
		degrees[key] = len(incoming[key])
		if degrees[key] == 0 {
			cycleQueue = append(cycleQueue, key)
		}
	}
	processed := 0
	for len(cycleQueue) > 0 {
		key := cycleQueue[0]
		cycleQueue = cycleQueue[1:]
		processed++
		for _, edge := range outgoing[key] {
			degrees[edge.To]--
			if degrees[edge.To] == 0 {
				cycleQueue = append(cycleQueue, edge.To)
			}
		}
	}
	if processed != len(nodes) {
		return errors.New("workflow graph must be acyclic")
	}
	for key, node := range nodes {
		if node.Kind != "end" && len(outgoing[key]) == 0 {
			return fmt.Errorf("node %q requires an outgoing edge", key)
		}
		if key != startKey && len(incoming[key]) == 0 {
			return fmt.Errorf("node %q requires an incoming edge", key)
		}
		if key == startKey && len(incoming[key]) > 0 {
			return errors.New("start node cannot have incoming edges")
		}
		if node.Kind == "end" && len(outgoing[key]) > 0 {
			return fmt.Errorf("end node %q cannot have outgoing edges", key)
		}
		switch node.Kind {
		case "gateway":
			if len(outgoing[key]) < 2 {
				return fmt.Errorf("gateway %q requires at least two outgoing edges", key)
			}
			defaultCount := 0
			for _, edge := range outgoing[key] {
				if edge.Default {
					defaultCount++
					if hasJSONValue(edge.Condition) {
						return fmt.Errorf("gateway %q default edge cannot have a condition", key)
					}
					continue
				}
				if !hasJSONValue(edge.Condition) {
					return fmt.Errorf("gateway %q non-default edge requires a condition", key)
				}
				if err := ValidateCondition(edge.Condition, nodes); err != nil {
					return fmt.Errorf("gateway %q edge to %q: %w", key, edge.To, err)
				}
				references, err := ConditionReferences(edge.Condition)
				if err != nil {
					return fmt.Errorf("gateway %q edge to %q: %w", key, edge.To, err)
				}
				for _, reference := range references {
					if !workflowPathExists(reference.Node, key, edges) {
						return fmt.Errorf(
							"gateway %q condition node %q must be upstream",
							key,
							reference.Node,
						)
					}
				}
			}
			if defaultCount != 1 {
				return fmt.Errorf("gateway %q requires exactly one default edge", key)
			}
		case "parallel_split":
			if len(outgoing[key]) < 2 {
				return fmt.Errorf("parallel split %q requires at least two outgoing edges", key)
			}
		case "parallel_join":
			if node.JoinMode != "all" && node.JoinMode != "any" {
				return fmt.Errorf("parallel join %q join_mode must be all or any", key)
			}
			if len(incoming[key]) < 2 {
				return fmt.Errorf("parallel join %q requires at least two incoming edges", key)
			}
			if len(outgoing[key]) != 1 {
				return fmt.Errorf("parallel join %q requires exactly one outgoing edge", key)
			}
		case "start", "activity":
			// Multiple plain outgoing edges are an implicit parallel split.
		default:
			if len(outgoing[key]) > 1 {
				return fmt.Errorf("node %q requires a gateway or parallel_split for multiple outgoing edges", key)
			}
		}
		if node.Kind != "activity" &&
			node.Kind != "end" &&
			node.Kind != "parallel_join" &&
			len(incoming[key]) > 1 {
			return fmt.Errorf("node %q requires a parallel_join for multiple incoming edges", key)
		}
		if node.Kind != "gateway" {
			for _, edge := range outgoing[key] {
				if edge.Default || hasJSONValue(edge.Condition) {
					return fmt.Errorf("node %q cannot declare conditional/default edges", key)
				}
			}
		}
	}

	visited := map[string]bool{}
	queue := []string{startKey}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if visited[key] {
			continue
		}
		visited[key] = true
		for _, edge := range outgoing[key] {
			queue = append(queue, edge.To)
		}
	}
	if len(visited) != len(nodes) {
		return errors.New("all nodes must be reachable from start")
	}

	degrees = make(map[string]int, len(nodes))
	for key := range nodes {
		degrees[key] = len(incoming[key])
	}
	queue = queue[:0]
	for key, degree := range degrees {
		if degree == 0 {
			queue = append(queue, key)
		}
	}
	processed = 0
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		processed++
		for _, edge := range outgoing[key] {
			degrees[edge.To]--
			if degrees[edge.To] == 0 {
				queue = append(queue, edge.To)
			}
		}
	}
	if processed != len(nodes) {
		return errors.New("workflow graph must be acyclic")
	}
	return nil
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

func validateAcceptance(
	acceptance AcceptanceDefinition,
	nodes map[string]NodeDefinition,
	roles map[string]RoleDefinition,
	edges []EdgeDefinition,
) error {
	if acceptance.Policy == "" || acceptance.Policy == "none" {
		return nil
	}
	if acceptance.Policy != "member" && acceptance.Policy != "node_verdict" {
		return fmt.Errorf("invalid acceptance policy %q", acceptance.Policy)
	}
	node, ok := nodes[acceptance.NodeKey]
	if !ok || node.Kind != "activity" || node.ActivityMode != "acceptance" {
		return errors.New("acceptance.node_key must reference a visible acceptance activity")
	}
	if acceptance.Policy == "member" {
		role, ok := roles[acceptance.ApproverRole]
		if !ok {
			return fmt.Errorf("acceptance references unknown approver role %q", acceptance.ApproverRole)
		}
		if !roleResolvesOnlyToMember(role) {
			return errors.New("acceptance approver role must resolve only to member")
		}
	}
	for _, target := range acceptance.ReworkTargets {
		node, ok := nodes[target]
		if !ok || node.Kind != "activity" {
			return fmt.Errorf("acceptance rework target %q must reference an activity", target)
		}
		if !workflowPathExists(target, acceptance.NodeKey, edges) {
			return fmt.Errorf(
				"acceptance rework target %q must reach acceptance node %q",
				target,
				acceptance.NodeKey,
			)
		}
	}
	return nil
}

func workflowPathExists(from, to string, edges []EdgeDefinition) bool {
	outgoing := map[string][]string{}
	for _, edge := range edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge.To)
	}
	visited := map[string]bool{}
	queue := []string{from}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == to {
			return true
		}
		if visited[current] {
			continue
		}
		visited[current] = true
		queue = append(queue, outgoing[current]...)
	}
	return false
}

func validKey(value string) bool {
	if value == "" || len(value) > 100 {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9') || (i > 0 && r == '_') {
			continue
		}
		return false
	}
	return true
}

// PinsMemberOwner reports whether the node designates a concrete member as
// its owner through a fixed_actor executor strategy. Activation turns that
// actor into the node's "owner" participant, which is what owner
// confirmations and node-owner permissions read.
func PinsMemberOwner(node NodeDefinition) bool {
	if node.OwnerRole != "" {
		return false
	}
	for _, strategy := range node.Executor.Strategies {
		if strategy.Kind == "fixed_actor" && strategy.ActorType == "member" {
			return true
		}
	}
	return false
}

func roleResolvesOnlyToMember(role RoleDefinition) bool {
	return len(role.AllowedActorTypes) == 1 &&
		role.AllowedActorTypes[0] == "member"
}

func validateIssueTitleTemplate(title string) error {
	remainder := strings.ReplaceAll(title, "{{host.title}}", "")
	if strings.Contains(remainder, "{{") || strings.Contains(remainder, "}}") {
		return errors.New("title template only supports {{host.title}}")
	}
	return nil
}
