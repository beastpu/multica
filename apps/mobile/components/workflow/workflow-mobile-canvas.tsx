import { Image } from "expo-image";
import { Pressable, ScrollView, View } from "react-native";
import type {
  WorkflowDefinition,
  WorkflowNodeInstance,
} from "@multica/core/workflows";
import { Text } from "@/components/ui/text";
import { cn } from "@/lib/utils";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { workflowCanvasColumns } from "@/lib/workflow-display";
import { WorkflowStatus } from "./workflow-status";

/**
 * Read-only mobile projection of the shared DAG canvas. A horizontal column
 * axis preserves sequence; activities in the same topological rank stack
 * vertically so parallel branches do not collapse into a false serial flow.
 */
export function WorkflowMobileCanvas({
  definition,
  nodes,
  selectedId,
  onSelect,
}: {
  definition?: WorkflowDefinition;
  nodes: WorkflowNodeInstance[];
  selectedId: string;
  onSelect: (id: string) => void;
}) {
  const columns = workflowCanvasColumns(definition, nodes);
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];

  if (columns.length === 0) {
    return (
      <View className="min-h-24 items-center justify-center rounded-md border border-dashed border-border">
        <Text className="text-sm text-muted-foreground">No activities</Text>
      </View>
    );
  }

  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      contentContainerClassName="items-center px-4 py-3"
      accessibilityRole="list"
      accessibilityLabel="Workflow activity map"
    >
      {columns.map((column, columnIndex) => (
        <View key={column.rank} className="flex-row items-center">
          {columnIndex > 0 ? (
            <Image
              source="sf:chevron.right"
              tintColor={theme.border}
              style={{ width: 10, height: 18, marginHorizontal: 8 }}
            />
          ) : null}
          <View className="gap-2">
            {column.nodes.map((node) => {
              const selected = node.id === selectedId;
              return (
                <Pressable
                  key={node.id}
                  onPress={() => onSelect(node.id)}
                  className={cn(
                    "min-h-16 w-40 justify-center rounded-md border bg-card px-3 py-2 active:bg-secondary",
                    selected ? "border-brand" : "border-border",
                  )}
                  accessibilityRole="button"
                  accessibilityState={{ selected }}
                  accessibilityLabel={`${node.name}, ${node.status}, attempt ${node.attempt}`}
                >
                  <Text className="text-sm font-medium" numberOfLines={2}>
                    {node.name}
                  </Text>
                  <View className="mt-1.5 flex-row items-center justify-between gap-2">
                    <WorkflowStatus status={node.status} compact />
                    {node.attempt > 1 ? (
                      <Text className="text-xs text-muted-foreground">
                        #{node.attempt}
                      </Text>
                    ) : null}
                  </View>
                </Pressable>
              );
            })}
          </View>
        </View>
      ))}
    </ScrollView>
  );
}
