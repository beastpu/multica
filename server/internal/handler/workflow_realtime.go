package handler

import (
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) publishWorkflowRealtime(
	eventType string,
	workspaceID string,
	actorType string,
	actorID string,
	payload map[string]any,
) {
	if h.Bus == nil {
		return
	}
	h.Bus.Publish(events.Event{
		Type: eventType, WorkspaceID: workspaceID,
		ActorType: actorType, ActorID: actorID, Payload: payload,
	})
}

func workflowRealtimePayload(instanceID, nodeID string) map[string]any {
	payload := map[string]any{}
	if instanceID != "" {
		payload["workflow_instance_id"] = instanceID
	}
	if nodeID != "" {
		payload["workflow_node_instance_id"] = nodeID
	}
	return payload
}

func (h *Handler) publishWorkflowInstanceUpdated(
	workspaceID, actorType, actorID, instanceID, nodeID string,
) {
	h.publishWorkflowRealtime(
		protocol.EventWorkflowInstanceUpdated,
		workspaceID, actorType, actorID,
		workflowRealtimePayload(instanceID, nodeID),
	)
}

func (h *Handler) publishWorkflowNodeUpdated(
	workspaceID, actorType, actorID, instanceID, nodeID string,
) {
	h.publishWorkflowRealtime(
		protocol.EventWorkflowNodeUpdated,
		workspaceID, actorType, actorID,
		workflowRealtimePayload(instanceID, nodeID),
	)
}

func (h *Handler) publishWorkflowTaskUpdated(
	workspaceID, actorType, actorID, instanceID, nodeID, taskID string,
) {
	payload := workflowRealtimePayload(instanceID, nodeID)
	payload["workflow_node_task_id"] = taskID
	h.publishWorkflowRealtime(
		protocol.EventWorkflowNodeTaskUpdated,
		workspaceID, actorType, actorID, payload,
	)
}
