import { Image } from "expo-image";
import { View } from "react-native";
import { Text } from "@/components/ui/text";
import { THEME } from "@/lib/theme";
import { useColorScheme } from "@/lib/use-color-scheme";

const LABELS: Record<string, string> = {
  draft: "Draft",
  pending: "Pending",
  needs_setup: "Needs setup",
  running: "Running",
  active: "Active",
  waiting: "Waiting",
  blocked: "Blocked",
  completed: "Completed",
  skipped: "Skipped",
  paused: "Paused",
  cancelled: "Cancelled",
  failed: "Failed",
  pending_materialization: "Pending issue",
  materializing: "Creating issue",
  materialized: "Issue created",
};

function statusVisual(status: string) {
  if (status === "completed" || status === "materialized") {
    return { symbol: "checkmark.circle.fill", tone: "success" as const };
  }
  if (status === "running" || status === "active") {
    return { symbol: "play.circle.fill", tone: "brand" as const };
  }
  if (
    status === "waiting" ||
    status === "pending" ||
    status === "pending_materialization" ||
    status === "materializing" ||
    status === "paused"
  ) {
    return { symbol: "clock.fill", tone: "warning" as const };
  }
  if (
    status === "blocked" ||
    status === "failed" ||
    status === "needs_setup"
  ) {
    return {
      symbol: "exclamationmark.triangle.fill",
      tone: "destructive" as const,
    };
  }
  if (status === "skipped" || status === "cancelled") {
    return { symbol: "minus.circle.fill", tone: "mutedForeground" as const };
  }
  return { symbol: "questionmark.circle", tone: "mutedForeground" as const };
}

export function WorkflowStatus({
  status,
  compact = false,
}: {
  status: string;
  compact?: boolean;
}) {
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const visual = statusVisual(status);
  return (
    <View
      className="flex-row items-center gap-1.5"
      accessibilityLabel={`Status: ${LABELS[status] ?? status}`}
    >
      <Image
        source={`sf:${visual.symbol}`}
        tintColor={theme[visual.tone]}
        style={{ width: compact ? 13 : 15, height: compact ? 13 : 15 }}
      />
      <Text
        className={compact ? "text-xs text-muted-foreground" : "text-sm"}
      >
        {LABELS[status] ?? status}
      </Text>
    </View>
  );
}
