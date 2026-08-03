package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
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
)

type Definition struct {
	SchemaVersion int                  `json:"schema_version"`
	Name          string               `json:"name"`
	Roles         []RoleDefinition     `json:"roles"`
	Nodes         []NodeDefinition     `json:"nodes"`
	Edges         []EdgeDefinition     `json:"edges"`
	Acceptance    AcceptanceDefinition `json:"acceptance"`
	Layout        json.RawMessage      `json:"layout,omitempty"`
}

type RoleDefinition struct {
	Key               string   `json:"key"`
	Name              string   `json:"name"`
	Required          bool     `json:"required"`
	AllowedActorTypes []string `json:"allowed_actor_types"`
}

type NodeDefinition struct {
	Key              string                `json:"key"`
	Kind             string                `json:"kind"`
	JoinMode         string                `json:"join_mode,omitempty"`
	Name             string                `json:"name"`
	Description      string                `json:"description,omitempty"`
	Color            string                `json:"color,omitempty"`
	TimeoutMinutes   int                   `json:"timeout_minutes,omitempty"`
	OwnerRole        string                `json:"owner_role,omitempty"`
	Executor         *ExecutorDefinition   `json:"executor,omitempty"`
	Reviewer         *ReviewerDefinition   `json:"reviewer,omitempty"`
	IssuePolicy      string                `json:"issue_policy,omitempty"`
	IssueTemplates   []IssueTemplate       `json:"issue_templates,omitempty"`
	Artifacts        []ArtifactRequirement `json:"artifacts,omitempty"`
	SubmissionSchema *SubmissionSchema     `json:"submission_schema,omitempty"`
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

// ExecutorDefinition names who is expected to produce the node's output. It is
// the single entry point: an issue template no longer carries an assignee, so
// there is exactly one place to read and one answer to give.
//
// Fallback is one layer deep and cannot nest. A chain longer than "who, and
// who instead" was available before and never used past two links, and each
// extra link is another rule the template author has to keep in their head.
type ExecutorDefinition struct {
	Kind       string              `json:"kind,omitempty"`
	Role       string              `json:"role,omitempty"`
	ActorType  string              `json:"actor_type,omitempty"`
	ActorID    string              `json:"actor_id,omitempty"`
	Capability string              `json:"capability,omitempty"`
	Fallback   *ExecutorDefinition `json:"fallback,omitempty"`
}

// HasExecutor reports whether the node names an executor at all. Absent means
// manual pickup, which is also what an unresolvable one degrades to.
//
// The field is a pointer because encoding/json ignores omitempty on a struct:
// a value type would put "executor":{} on every start and end node, which is
// meaningless in the stored definition and broke the client contract once
// kind became required.
func HasExecutor(executor *ExecutorDefinition) bool {
	return executor != nil && strings.TrimSpace(executor.Kind) != ""
}

// ReviewerDefinition names who judges the node's output. Absent means the node
// completes on delivery; present means delivery moves the node to in_review and
// the reviewer's verdict is what releases it.
type ReviewerDefinition struct {
	Kind      string `json:"kind"`
	Role      string `json:"role,omitempty"`
	ActorType string `json:"actor_type,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	// APIURL is the endpoint an "api" reviewer calls. It is template
	// configuration, never taken from a submission or an agent, because an
	// address supplied by the thing under review would let it choose its own
	// judge.
	APIURL string `json:"api_url,omitempty"`
	// Condition carries an "auto" reviewer's rule, evaluated against upstream
	// submissions, verdicts, and host fields.
	Condition json.RawMessage `json:"condition,omitempty"`
	Required  bool            `json:"required,omitempty"`
}

// HasCondition reports whether a raw condition document carries a value.
func HasCondition(raw json.RawMessage) bool {
	return hasJSONValue(raw)
}

// ReviewerAcceptsActor reports whether a member or agent records this node's verdict.
// An api or auto reviewer decides on its own, so a member posting a verdict
// there would be overruling a judge the template chose deliberately.
func ReviewerAcceptsActor(node NodeDefinition) bool {
	if node.Reviewer == nil {
		return false
	}
	switch node.Reviewer.Kind {
	case "role", "actor", "owner":
		return true
	default:
		return false
	}
}

// RequiresReview reports whether delivery must wait for a verdict. A reviewer
// that is present but not required may still record one; it just does not hold
// the node.
func RequiresReview(node NodeDefinition) bool {
	return node.Reviewer != nil && node.Reviewer.Required
}

type IssueTemplate struct {
	Key           string `json:"key"`
	Title         string `json:"title"`
	Description   string `json:"description,omitempty"`
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

// SubmissionSchema carries only how many results a node submits. The
// user-defined field list is gone: a node's business output is an artifact, its
// conclusion is the handoff summary, and its branch decision is a node choice —
// three system-defined shapes that cover what fields were used for, without
// asking a template author to design a form per node.
type SubmissionSchema struct {
	Policy string `json:"policy,omitempty"`
}

type CompletionDefinition struct {
	Mode                 string `json:"mode,omitempty"`
	RequiredIssueOutcome string `json:"required_issue_outcome,omitempty"`
	SubmissionRequired   bool   `json:"submission_required,omitempty"`
	// HandoffRequired blocks completion until the node carries a non-empty
	// handoff summary. It is separate from SubmissionRequired because a node
	// can owe a structured result without owing a conclusion, and far more
	// often owes the conclusion alone.
	HandoffRequired bool `json:"handoff_required,omitempty"`
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

// AcceptanceDefinition gates the run as a whole, not any one node. It has no
// node_key: acceptance judges whether the requirement is done, which is a
// property of the instance and its host issue, and giving it a position on the
// canvas only ever meant an activity nobody worked.
//
// It also has no rework_targets. Where a rejection sends the flow is a property
// of the graph — any activity that precedes the rejection point — not a list
// the template maintains alongside the graph and forgets to update.
type AcceptanceDefinition struct {
	Policy       string `json:"policy,omitempty"`
	ApproverRole string `json:"approver_role,omitempty"`
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
	if err := validateReviewerConditions(definition.Nodes, nodes, definition.Edges); err != nil {
		return err
	}
	if err := validateAcceptance(definition.Acceptance, roles); err != nil {
		return err
	}
	return nil
}

// NormalizeAuthoringDefinition folds the two legacy sign-off gates into the
// node reviewer before a new immutable workflow version is written. Runtime
// parsing intentionally does not call this function: published versions and
// runs that still carry workflow-level acceptance or manual completion keep
// their original behavior until they finish.
func NormalizeAuthoringDefinition(definition Definition) (Definition, error) {
	for index := range definition.Nodes {
		node := &definition.Nodes[index]
		if node.Kind != "activity" {
			continue
		}
		requiresManualReview := node.Completion.Mode == "manual" ||
			(node.Completion.Mode == "" && RequiresManualCompletion(*node) &&
				(node.OwnerRole != "" || PinsMemberOwner(*node)))
		if node.Reviewer != nil && node.Reviewer.Kind == "owner" {
			reviewer, err := reviewerFromLegacyOwner(*node)
			if err != nil {
				return Definition{}, err
			}
			node.Reviewer = reviewer
		}
		if requiresManualReview && node.Reviewer == nil {
			reviewer, err := reviewerFromLegacyOwner(*node)
			if err != nil {
				return Definition{}, fmt.Errorf(
					"activity %q manual completion requires a reviewer: %w",
					node.Key,
					err,
				)
			}
			node.Reviewer = reviewer
		}
		if node.Reviewer != nil {
			node.Reviewer.Required = true
		}
		node.Completion.Mode = "automatic"
	}
	if definition.Acceptance.Policy == "member" {
		terminalActivities := terminalActivityIndexes(definition)
		if len(terminalActivities) == 0 {
			return Definition{}, errors.New(
				"member acceptance requires a terminal activity reviewer",
			)
		}
		for _, index := range terminalActivities {
			node := &definition.Nodes[index]
			switch {
			case node.Reviewer == nil:
				node.Reviewer = &ReviewerDefinition{
					Kind: "role", Role: definition.Acceptance.ApproverRole,
					Required: true,
				}
			default:
				// A node reviewer is visible and editable in the current authoring
				// model, so it wins over the removed workflow-level acceptance
				// field. Acceptance only fills a missing terminal reviewer.
				node.Reviewer.Required = true
			}
		}
	}
	definition.Acceptance = AcceptanceDefinition{}
	if err := ValidateDefinition(definition); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

func terminalActivityIndexes(definition Definition) []int {
	nodeIndexes := make(map[string]int, len(definition.Nodes))
	incoming := make(map[string][]string, len(definition.Nodes))
	queue := make([]string, 0)
	for index, node := range definition.Nodes {
		nodeIndexes[node.Key] = index
		if node.Kind == "end" {
			queue = append(queue, node.Key)
		}
	}
	for _, edge := range definition.Edges {
		incoming[edge.To] = append(incoming[edge.To], edge.From)
	}
	seen := make(map[string]struct{}, len(definition.Nodes))
	terminal := make(map[int]struct{})
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if _, visited := seen[key]; visited {
			continue
		}
		seen[key] = struct{}{}
		index, exists := nodeIndexes[key]
		if !exists {
			continue
		}
		if definition.Nodes[index].Kind == "activity" {
			terminal[index] = struct{}{}
			continue
		}
		queue = append(queue, incoming[key]...)
	}
	indexes := make([]int, 0, len(terminal))
	for index := range definition.Nodes {
		if _, exists := terminal[index]; exists {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func reviewerFromLegacyOwner(node NodeDefinition) (*ReviewerDefinition, error) {
	if node.OwnerRole != "" {
		return &ReviewerDefinition{
			Kind: "role", Role: node.OwnerRole, Required: true,
		}, nil
	}
	if PinsMemberOwner(node) {
		return &ReviewerDefinition{
			Kind: "actor", ActorType: "member", ActorID: node.Executor.ActorID,
			Required: true,
		}, nil
	}
	return nil, errors.New("legacy node owner is not assigned")
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
	if node.TimeoutMinutes < 0 || node.TimeoutMinutes > 525600 {
		return fmt.Errorf("activity %q has invalid timeout_minutes", node.Key)
	}
	if node.OwnerRole != "" {
		if _, ok := roles[node.OwnerRole]; !ok {
			return fmt.Errorf("activity %q references unknown owner role %q", node.Key, node.OwnerRole)
		}
	}
	if err := validateExecutor(node, roles); err != nil {
		return err
	}
	if err := validateReviewer(node, roles); err != nil {
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
		default:
			return fmt.Errorf(
				"activity %q has invalid submission policy %q",
				node.Key,
				node.SubmissionSchema.Policy,
			)
		}
		if node.IssuePolicy == "none" &&
			(policy == "per_required_task" || policy == "fan_in") {
			return fmt.Errorf(
				"activity %q submission policy %q requires issue-backed tasks",
				node.Key,
				policy,
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
	for _, roleKey := range node.Completion.AuthorizedRoles {
		if _, ok := roles[roleKey]; !ok {
			return fmt.Errorf(
				"activity %q completion references unknown authorized role %q",
				node.Key,
				roleKey,
			)
		}
	}
	return nil
}

// validateReviewer checks the one field that says who judges the node. The
// per-kind rules are the same ones the old verdict and confirmation switches
// enforced separately: an api reviewer needs a template-supplied https endpoint,
// an owner reviewer needs an owner that resolves to a member, and an auto
// reviewer is the only kind that carries a rule instead of a person.
func validateReviewer(node NodeDefinition, roles map[string]RoleDefinition) error {
	reviewer := node.Reviewer
	if reviewer == nil {
		return nil
	}
	switch reviewer.Kind {
	case "role":
		if _, ok := roles[reviewer.Role]; !ok {
			return fmt.Errorf(
				"activity %q reviewer references unknown role %q", node.Key, reviewer.Role,
			)
		}
	case "actor":
		if err := validateDirectActor(reviewer.ActorType, reviewer.ActorID); err != nil {
			return fmt.Errorf("activity %q reviewer: %w", node.Key, err)
		}
		if reviewer.ActorType == "" {
			return fmt.Errorf("activity %q actor reviewer requires an actor", node.Key)
		}
	case "api":
		if err := validateReviewerAPIURL(node.Key, reviewer.APIURL); err != nil {
			return err
		}
	case "owner":
		// The owner is a runtime participant, so either a member-only owner
		// role or a directly pinned member executor can carry the review.
		if node.OwnerRole == "" {
			if !PinsMemberOwner(node) {
				return fmt.Errorf(
					"activity %q owner reviewer requires an owner role or a pinned member owner",
					node.Key,
				)
			}
			break
		}
		role, ok := roles[node.OwnerRole]
		if !ok {
			return fmt.Errorf("activity %q owner reviewer requires owner_role", node.Key)
		}
		if !roleResolvesOnlyToMember(role) {
			return fmt.Errorf(
				"activity %q owner reviewer role must resolve only to member", node.Key,
			)
		}
	case "auto":
		if !hasJSONValue(reviewer.Condition) {
			return fmt.Errorf("activity %q auto reviewer requires a condition", node.Key)
		}
	default:
		return fmt.Errorf("activity %q has invalid reviewer kind %q", node.Key, reviewer.Kind)
	}
	if reviewer.Kind != "api" && strings.TrimSpace(reviewer.APIURL) != "" {
		return fmt.Errorf("activity %q only an api reviewer can declare api_url", node.Key)
	}
	if reviewer.Kind != "auto" && hasJSONValue(reviewer.Condition) {
		return fmt.Errorf("activity %q only an auto reviewer can declare condition", node.Key)
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
	if node.Kind != "activity" {
		return false
	}
	switch node.Completion.Mode {
	case "manual":
		return true
	case "automatic":
		return false
	}
	if node.SubmissionSchema != nil || node.Completion.SubmissionRequired ||
		node.Reviewer != nil {
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
	if !HasExecutor(node.Executor) {
		if node.Executor != nil && node.Executor.Fallback != nil {
			return fmt.Errorf("activity %q fallback requires an executor", node.Key)
		}
		return nil
	}
	if err := validateExecutorEntry(node.Key, "executor", *node.Executor, roles); err != nil {
		return err
	}
	fallback := node.Executor.Fallback
	if fallback == nil {
		return nil
	}
	if fallback.Fallback != nil {
		return fmt.Errorf("activity %q executor fallback cannot declare its own fallback", node.Key)
	}
	if !HasExecutor(fallback) {
		return fmt.Errorf("activity %q executor fallback requires a kind", node.Key)
	}
	return validateExecutorEntry(node.Key, "executor fallback", *fallback, roles)
}

func validateExecutorEntry(
	nodeKey string,
	label string,
	executor ExecutorDefinition,
	roles map[string]RoleDefinition,
) error {
	switch executor.Kind {
	case "role":
		if _, ok := roles[executor.Role]; !ok {
			return fmt.Errorf("activity %q %s references unknown role %q", nodeKey, label, executor.Role)
		}
	case "actor":
		if err := validateDirectActor(executor.ActorType, executor.ActorID); err != nil {
			return fmt.Errorf("activity %q %s: %w", nodeKey, label, err)
		}
		if executor.ActorType == "" {
			return fmt.Errorf("activity %q %s requires an actor", nodeKey, label)
		}
	case "capability":
		if strings.TrimSpace(executor.Capability) == "" {
			return fmt.Errorf("activity %q %s requires a capability", nodeKey, label)
		}
		role, ok := roles[executor.Role]
		if !ok {
			return fmt.Errorf(
				"activity %q %s references unknown pool role %q", nodeKey, label, executor.Role,
			)
		}
		allowedPoolActor := false
		for _, actorType := range role.AllowedActorTypes {
			allowedPoolActor = allowedPoolActor || actorType == "agent" || actorType == "squad"
		}
		if !allowedPoolActor {
			return fmt.Errorf(
				"activity %q %s pool role must allow agent or squad", nodeKey, label,
			)
		}
	case "manual":
	default:
		return fmt.Errorf("activity %q has invalid %s kind %q", nodeKey, label, executor.Kind)
	}
	if executor.Kind != "capability" && strings.TrimSpace(executor.Capability) != "" {
		return fmt.Errorf("activity %q only a capability %s can declare capability", nodeKey, label)
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

func validateReviewerConditions(
	nodeDefinitions []NodeDefinition,
	nodes map[string]NodeDefinition,
	edges []EdgeDefinition,
) error {
	for _, node := range nodeDefinitions {
		if node.Reviewer == nil || !hasJSONValue(node.Reviewer.Condition) {
			continue
		}
		if err := ValidateCondition(node.Reviewer.Condition, nodes); err != nil {
			return fmt.Errorf("activity %q auto reviewer condition: %w", node.Key, err)
		}
		references, err := ConditionReferences(node.Reviewer.Condition)
		if err != nil {
			return fmt.Errorf("activity %q auto reviewer condition: %w", node.Key, err)
		}
		for _, reference := range references {
			if reference.Node == node.Key {
				if reference.Source == "node_verdict" {
					return fmt.Errorf(
						"activity %q auto reviewer cannot reference itself", node.Key,
					)
				}
				continue
			}
			if !workflowPathExists(reference.Node, node.Key, edges) {
				return fmt.Errorf(
					"activity %q auto reviewer node %q must be upstream", node.Key, reference.Node,
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
	roles map[string]RoleDefinition,
) error {
	if acceptance.Policy == "" || acceptance.Policy == "none" {
		return nil
	}
	if acceptance.Policy != "member" && acceptance.Policy != "node_verdict" {
		return fmt.Errorf("invalid acceptance policy %q", acceptance.Policy)
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
// its owner through an actor executor. Activation turns that actor into the
// node's "owner" participant, which is what an owner reviewer and node-owner
// permissions read.
func PinsMemberOwner(node NodeDefinition) bool {
	if node.OwnerRole != "" {
		return false
	}
	return node.Executor != nil && node.Executor.Kind == "actor" &&
		node.Executor.ActorType == "member"
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

// validateReviewerAPIURL keeps an api reviewer pointed at a real external
// endpoint. The scheme check is the meaningful one: anything but https would
// send the workflow's state over a channel the workspace cannot vouch for.
func validateReviewerAPIURL(nodeKey, raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fmt.Errorf("activity %q api reviewer requires api_url", nodeKey)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("activity %q has invalid api_url: %w", nodeKey, err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("activity %q api_url must use https", nodeKey)
	}
	if parsed.Host == "" {
		return fmt.Errorf("activity %q api_url is missing a host", nodeKey)
	}
	return nil
}

// ReachableFrom returns every node the graph can reach from `from`, excluding
// `from` itself. Exported so callers can bound a value by what the graph allows
// rather than by a list they maintain separately.
//
// One traversal rather than one per candidate: the caller wants the whole set,
// and asking "can I reach X" node by node walks the graph again for each.
func ReachableFrom(plan GraphPlan, from string) map[string]struct{} {
	reachable := map[string]struct{}{}
	queue := []string{from}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range plan.Outgoing[current] {
			if _, seen := reachable[edge.To]; seen || edge.To == from {
				continue
			}
			reachable[edge.To] = struct{}{}
			queue = append(queue, edge.To)
		}
	}
	return reachable
}
