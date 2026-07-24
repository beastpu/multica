/**
 * Mobile Workflow v1: read-only Canvas / Node / issue surface.
 *
 * Product semantics mirror the shared Web/Desktop Workbench. The phone
 * intentionally linearizes the DAG into horizontally scrollable topological
 * columns and exposes no template editing or runtime transitions.
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  RefreshControl,
  ScrollView,
  View,
} from "react-native";
import SegmentedControl from "@react-native-segmented-control/segmented-control";
import { Stack, router, useLocalSearchParams } from "expo-router";
import { useQuery } from "@tanstack/react-query";
import type {
  WorkflowNodeDetail,
  WorkflowNodeInstance,
  WorkflowNodeParticipant,
} from "@multica/core/workflows";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { IssueRow } from "@/components/issue/issue-row";
import { ActorAvatar } from "@/components/ui/actor-avatar";
import { WorkflowMobileCanvas } from "@/components/workflow/workflow-mobile-canvas";
import { WorkflowStatus } from "@/components/workflow/workflow-status";
import {
  WORKFLOWS_ACTIVITY_ENGINE_FLAG,
  workflowFeatureOptions,
  workflowInstanceIssuesOptions,
  workflowInstanceOptions,
  workflowNodeOptions,
  workflowTemplateOptions,
} from "@/data/queries/workflows";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useActorLookup } from "@/data/use-actor-name";
import { useWorkflowRealtime } from "@/data/realtime/use-workflow-realtime";
import {
  latestWorkflowAttemptNodes,
  workflowIssuesForScope,
  type WorkflowIssueScope,
} from "@/lib/workflow-display";

export default function WorkflowReadOnlyScreen() {
  const { id: instanceId, workspace: wsSlug } = useLocalSearchParams<{
    id: string;
    workspace: string;
  }>();
  const wsId = useWorkspaceStore((state) => state.currentWorkspaceId);
  const featureQuery = useQuery(workflowFeatureOptions(wsId));
  const enabled =
    featureQuery.data?.feature_flags[WORKFLOWS_ACTIVITY_ENGINE_FLAG] ?? false;
  useWorkflowRealtime(enabled);
  const detailQuery = useQuery({
    ...workflowInstanceOptions(wsId, instanceId),
    enabled: enabled && !!wsId && !!instanceId,
  });
  const instance = detailQuery.data?.instance;
  const nodes = useMemo(
    () => latestWorkflowAttemptNodes(detailQuery.data?.nodes ?? []),
    [detailQuery.data?.nodes],
  );
  const [selectedNodeId, setSelectedNodeId] = useState("");
  const [issueScope, setIssueScope] = useState<WorkflowIssueScope>("current");

  useEffect(() => {
    if (nodes.length === 0) return;
    if (nodes.some((node) => node.id === selectedNodeId)) return;
    const current = nodes.find(
      (node) =>
        node.status === "active" ||
        node.status === "waiting" ||
        node.status === "blocked",
    );
    setSelectedNodeId((current ?? nodes[0])!.id);
  }, [nodes, selectedNodeId]);

  const selectedNode = nodes.find((node) => node.id === selectedNodeId);
  const nodeQuery = useQuery({
    ...workflowNodeOptions(wsId, selectedNodeId),
    enabled: enabled && !!wsId && !!selectedNodeId,
  });
  const issuesQuery = useQuery({
    ...workflowInstanceIssuesOptions(wsId, instanceId),
    enabled: enabled && !!wsId && !!instanceId,
  });
  const templateQuery = useQuery(
    {
      ...workflowTemplateOptions(wsId, instance?.template_id ?? ""),
      enabled: enabled && !!wsId && !!instance?.template_id,
    },
  );
  const templateVersion = templateQuery.data?.versions.find(
    (version) => version.id === instance?.template_version_id,
  );
  const visibleIssues = workflowIssuesForScope(
    issuesQuery.data?.issues ?? [],
    detailQuery.data?.tasks ?? [],
    selectedNodeId,
    issueScope,
  );

  const refreshing =
    detailQuery.isRefetching ||
    nodeQuery.isRefetching ||
    issuesQuery.isRefetching ||
    templateQuery.isRefetching;
  const onRefresh = useCallback(async () => {
    await Promise.all([
      detailQuery.refetch(),
      issuesQuery.refetch(),
      templateQuery.refetch(),
      selectedNodeId ? nodeQuery.refetch() : Promise.resolve(),
    ]);
  }, [
    detailQuery,
    issuesQuery,
    nodeQuery,
    selectedNodeId,
    templateQuery,
  ]);

  if (featureQuery.isLoading || (enabled && detailQuery.isLoading)) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <Stack.Screen options={{ title: "Workflow" }} />
        <ActivityIndicator />
      </View>
    );
  }

  if (!enabled) {
    return (
      <View className="flex-1 items-center justify-center gap-3 bg-background px-6">
        <Stack.Screen options={{ title: "Workflow" }} />
        <Text className="text-center text-sm text-muted-foreground">
          Workflow is not enabled for this workspace.
        </Text>
        <Button variant="outline" onPress={() => router.back()}>
          <Text>Back</Text>
        </Button>
      </View>
    );
  }

  if (detailQuery.isError || !instance?.id) {
    return (
      <View className="flex-1 items-center justify-center gap-3 bg-background px-6">
        <Stack.Screen options={{ title: "Workflow" }} />
        <Text className="text-center text-sm text-destructive">
          Failed to load workflow.
        </Text>
        <Button variant="outline" onPress={() => detailQuery.refetch()}>
          <Text>Retry</Text>
        </Button>
      </View>
    );
  }

  return (
    <View className="flex-1 bg-background">
      <Stack.Screen
        options={{
          title: instance.host_issue_identifier || "Workflow",
          headerBackTitle: "Back",
        }}
      />
      <ScrollView
        refreshControl={
          <RefreshControl refreshing={refreshing} onRefresh={onRefresh} />
        }
        contentContainerClassName="pb-10"
      >
        <View className="px-4 pb-3 pt-4">
          <View className="flex-row items-start justify-between gap-3">
            <View className="min-w-0 flex-1">
              <Text className="text-lg font-semibold" numberOfLines={2}>
                {instance.host_issue_title || instance.template_name}
              </Text>
              <Text className="mt-1 text-xs text-muted-foreground">
                {instance.template_name} · v{instance.template_version}
              </Text>
            </View>
            <WorkflowStatus status={instance.status} />
          </View>
          <Text className="mt-3 text-sm text-muted-foreground">
            {instance.activity_completed}/{instance.activity_total} activities
            completed
          </Text>
        </View>

        <View className="border-y border-border bg-muted/20 py-2">
          <View className="px-4">
            <Text className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              Activity map
            </Text>
          </View>
          <WorkflowMobileCanvas
            definition={templateVersion?.definition}
            nodes={nodes}
            selectedId={selectedNodeId}
            onSelect={(id) => {
              setSelectedNodeId(id);
              setIssueScope("current");
            }}
          />
        </View>

        <View className="gap-4 px-4 pt-4">
          <NodeDetails
            selectedNode={selectedNode}
            detail={nodeQuery.data}
            loading={nodeQuery.isLoading}
          />

          <View>
            <Text className="mb-2 text-sm font-medium">Workflow issues</Text>
            <SegmentedControl
              values={["Current activity", "All workflow"]}
              selectedIndex={issueScope === "current" ? 0 : 1}
              onChange={(event) =>
                setIssueScope(
                  event.nativeEvent.selectedSegmentIndex === 0
                    ? "current"
                    : "all",
                )}
              accessibilityLabel="Workflow issue scope"
            />
            <Card className="mt-3 overflow-hidden p-0">
              {issuesQuery.isLoading ? (
                <View className="min-h-24 items-center justify-center">
                  <ActivityIndicator />
                </View>
              ) : visibleIssues.length === 0 ? (
                <View className="min-h-24 items-center justify-center px-4">
                  <Text className="text-center text-sm text-muted-foreground">
                    No issues in this scope
                  </Text>
                </View>
              ) : (
                visibleIssues.map((issue, index) => (
                  <View
                    key={issue.id}
                    className={index > 0 ? "border-t border-border" : undefined}
                  >
                    <IssueRow
                      issue={issue}
                      showStatus
                      onPress={() =>
                        router.push(`/${wsSlug}/issue/${issue.id}`)
                      }
                    />
                  </View>
                ))
              )}
            </Card>
          </View>
        </View>
      </ScrollView>
    </View>
  );
}

function NodeDetails({
  selectedNode,
  detail,
  loading,
}: {
  selectedNode: WorkflowNodeInstance | undefined;
  detail: WorkflowNodeDetail | undefined;
  loading: boolean;
}) {
  const { getName } = useActorLookup();

  if (!selectedNode) {
    return (
      <Card className="min-h-24 items-center justify-center">
        <Text className="text-sm text-muted-foreground">
          Select an activity
        </Text>
      </Card>
    );
  }
  if (loading || !detail?.node.id) {
    return (
      <Card className="min-h-24 items-center justify-center">
        <ActivityIndicator />
      </Card>
    );
  }

  const owners = detail.participants.filter(
    (participant) => participant.role === "owner",
  );
  const collaborators = detail.participants.filter(
    (participant) => participant.role !== "owner",
  );

  return (
    <View className="gap-3">
      <View className="flex-row items-start justify-between gap-3">
        <View className="min-w-0 flex-1">
          <Text className="text-base font-semibold">{selectedNode.name}</Text>
          <Text className="mt-1 text-xs text-muted-foreground">
            Attempt {selectedNode.attempt}
          </Text>
        </View>
        <WorkflowStatus status={selectedNode.status} />
      </View>

      <Card className="gap-4">
        {selectedNode.definition.description ? (
          <Text className="text-sm text-muted-foreground">
            {selectedNode.definition.description}
          </Text>
        ) : null}
        <ActorGroup
          title="Owner"
          participants={owners}
          getName={getName}
        />
        {collaborators.length > 0 ? (
          <ActorGroup
            title="Participants"
            participants={collaborators}
            getName={getName}
          />
        ) : null}
        {selectedNode.waiting_reasons.length > 0 ? (
          <View className="gap-1">
            <Text className="text-xs font-medium text-muted-foreground">
              Waiting for
            </Text>
            {selectedNode.waiting_reasons.map((reason, index) => (
              <Text
                key={`${reason.code}-${index}`}
                className="text-sm text-foreground"
              >
                • {reason.message || reason.code}
              </Text>
            ))}
          </View>
        ) : null}
      </Card>

      <Card className="gap-3">
        <Text className="text-sm font-medium">
          Tasks ({detail.tasks.length})
        </Text>
        {detail.tasks.length === 0 ? (
          <Text className="text-sm text-muted-foreground">No tasks</Text>
        ) : (
          detail.tasks.map((task) => (
            <View
              key={task.id}
              className="flex-row items-start justify-between gap-3 border-t border-border pt-3 first:border-t-0 first:pt-0"
            >
              <View className="min-w-0 flex-1">
                <Text className="text-sm" numberOfLines={2}>
                  {task.definition.title || task.task_key}
                </Text>
                <Text className="mt-1 text-xs text-muted-foreground">
                  {task.required ? "Required" : "Optional"}
                </Text>
              </View>
              <WorkflowStatus status={task.materialization_status} compact />
            </View>
          ))
        )}
      </Card>

      {(detail.submissions.length > 0 || detail.verdicts.length > 0) ? (
        <Card className="gap-3">
          <Text className="text-sm font-medium">Delivery record</Text>
          {detail.submissions.map((submission) => (
            <View key={submission.id} className="gap-1">
              <Text className="text-sm">
                Submission #{submission.revision}
              </Text>
              {submission.summary ? (
                <Text className="text-xs text-muted-foreground">
                  {submission.summary}
                </Text>
              ) : null}
            </View>
          ))}
          {detail.verdicts.map((verdict) => (
            <View key={verdict.id} className="gap-1 border-t border-border pt-3">
              <View className="flex-row items-center justify-between gap-2">
                <Text className="text-sm">Verdict #{verdict.revision}</Text>
                <WorkflowStatus status={verdict.result} compact />
              </View>
              {verdict.reason ? (
                <Text className="text-xs text-muted-foreground">
                  {verdict.reason}
                </Text>
              ) : null}
            </View>
          ))}
        </Card>
      ) : null}
    </View>
  );
}

function ActorGroup({
  title,
  participants,
  getName,
}: {
  title: string;
  participants: WorkflowNodeParticipant[];
  getName: ReturnType<typeof useActorLookup>["getName"];
}) {
  return (
    <View className="gap-2">
      <Text className="text-xs font-medium text-muted-foreground">{title}</Text>
      {participants.length === 0 ? (
        <Text className="text-sm text-muted-foreground">Unassigned</Text>
      ) : (
        participants.map((participant) => {
          const actorType =
            participant.actor_type === "member" ||
            participant.actor_type === "agent" ||
            participant.actor_type === "squad"
              ? participant.actor_type
              : null;
          return (
            <View key={participant.id} className="flex-row items-center gap-2">
              <ActorAvatar
                type={actorType}
                id={participant.actor_id}
                size={24}
              />
              <View className="min-w-0 flex-1">
                <Text className="text-sm" numberOfLines={1}>
                  {getName(actorType, participant.actor_id)}
                </Text>
                <Text className="text-xs text-muted-foreground">
                  {participant.role}
                </Text>
              </View>
            </View>
          );
        })
      )}
    </View>
  );
}
