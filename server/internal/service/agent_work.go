package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Agent derived-work ("agent_work") shared primitives
// (docs/agent-fix-p4-assessment-issue-design.md, Part 1).
//
// A derived-work projection issue is a normal issue whose authoritative
// marker is the reserved metadata key `agent_work`, stamped at creation by
// CreateAgentWorkIssue and never writable through the user metadata API. Each
// kind owns a lazily created system project per workspace, used purely for
// organization — guards and queries key on the metadata marker, never on
// project membership.

const (
	// AgentWorkMetadataKey is the reserved issue-metadata key marking a
	// derived-work projection issue. The user metadata API rejects it (set
	// and delete); only server-side projection code writes it.
	AgentWorkMetadataKey = "agent_work"

	// AgentWorkKindP4Assessment is the first derived-work kind: one projection
	// issue per P4 assessment run.
	AgentWorkKindP4Assessment = "p4_assessment"
)

// AgentWorkMetadata is the value stored under the reserved agent_work key.
type AgentWorkMetadata struct {
	Kind          string            `json:"kind"`
	SourceIssueID string            `json:"source_issue_id,omitempty"`
	SourceRef     string            `json:"source_ref,omitempty"`
	Trigger       string            `json:"trigger,omitempty"`
	Extra         map[string]string `json:"extra,omitempty"`
}

func agentWorkIssueMetadata(meta AgentWorkMetadata) ([]byte, error) {
	return json.Marshal(map[string]AgentWorkMetadata{AgentWorkMetadataKey: meta})
}

// ensureAgentWorkProject returns the system project for (workspace, kind),
// creating it lazily on first use. Must run inside a transaction: the
// advisory xact lock serializes concurrent creators across replicas so a lost
// race cannot leave an orphan project behind an ON CONFLICT DO NOTHING.
func ensureAgentWorkProject(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, kind, title string) (pgtype.UUID, error) {
	if err := q.LockAgentWorkProjectKind(ctx, db.LockAgentWorkProjectKindParams{
		WorkspaceID: util.UUIDToString(workspaceID),
		Kind:        kind,
	}); err != nil {
		return pgtype.UUID{}, fmt.Errorf("lock agent work project: %w", err)
	}
	projectID, err := q.GetAgentWorkProject(ctx, db.GetAgentWorkProjectParams{
		WorkspaceID: workspaceID,
		Kind:        kind,
	})
	if err == nil {
		return projectID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, err
	}
	project, err := q.CreateProject(ctx, db.CreateProjectParams{
		WorkspaceID: workspaceID,
		Title:       title,
		Description: pgtype.Text{String: "系统 project：Agent 派生工作（" + kind + "）的运行投影 issue。", Valid: true},
		Status:      "in_progress",
		Priority:    "none",
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create agent work project: %w", err)
	}
	if _, err := q.InsertAgentWorkProject(ctx, db.InsertAgentWorkProjectParams{
		WorkspaceID: workspaceID,
		Kind:        kind,
		ProjectID:   project.ID,
	}); err != nil {
		return pgtype.UUID{}, fmt.Errorf("insert agent work project mapping: %w", err)
	}
	return project.ID, nil
}
