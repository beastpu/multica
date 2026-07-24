import { Image } from "expo-image";
import { Pressable, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import { router } from "expo-router";
import { Text } from "@/components/ui/text";
import { useWorkspaceStore } from "@/data/workspace-store";
import {
  issueWorkflowOptions,
  WORKFLOWS_ACTIVITY_ENGINE_FLAG,
  workflowFeatureOptions,
} from "@/data/queries/workflows";
import { useWorkflowRealtime } from "@/data/realtime/use-workflow-realtime";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { WorkflowStatus } from "./workflow-status";

/**
 * Read-only Workflow affordance for host issues. A 404 is the normal result
 * for ordinary issues and intentionally renders nothing, preserving old
 * server compatibility and avoiding a false empty state.
 */
export function WorkflowHostCard({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceStore((state) => state.currentWorkspaceId);
  const wsSlug = useWorkspaceStore((state) => state.currentWorkspaceSlug);
  const featureQuery = useQuery(workflowFeatureOptions(wsId));
  const enabled =
    featureQuery.data?.feature_flags[WORKFLOWS_ACTIVITY_ENGINE_FLAG] ?? false;
  const query = useQuery({
    ...issueWorkflowOptions(wsId, issueId),
    enabled: enabled && !!wsId && !!issueId,
  });
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  useWorkflowRealtime(enabled);
  const instance = query.data?.instance;

  if (!instance?.id || !wsSlug) return null;

  const activity =
    instance.current_activities.map((item) => item.name).join(" · ") ||
    "No active activity";
  return (
    <Pressable
      onPress={() => router.push(`/${wsSlug}/workflow/${instance.id}`)}
      className="mx-4 mt-3 min-h-16 rounded-md border border-border bg-card p-3 active:bg-secondary"
      accessibilityRole="button"
      accessibilityLabel={`Open workflow ${instance.template_name}`}
    >
      <View className="flex-row items-center gap-3">
        <View className="size-9 items-center justify-center rounded-md bg-brand/10">
          <Image
            source="sf:point.3.connected.trianglepath.dotted"
            tintColor={theme.brand}
            style={{ width: 20, height: 20 }}
          />
        </View>
        <View className="min-w-0 flex-1">
          <View className="flex-row items-center justify-between gap-2">
            <Text className="min-w-0 flex-1 text-sm font-medium" numberOfLines={1}>
              {instance.template_name || "Workflow"}
            </Text>
            <WorkflowStatus status={instance.status} compact />
          </View>
          <Text className="mt-1 text-xs text-muted-foreground" numberOfLines={1}>
            {activity} · {instance.activity_completed}/{instance.activity_total}
          </Text>
        </View>
        <Image
          source="sf:chevron.right"
          tintColor={theme.mutedForeground}
          style={{ width: 9, height: 14 }}
        />
      </View>
    </Pressable>
  );
}
