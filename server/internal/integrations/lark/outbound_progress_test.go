package lark

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestProjectTaskProgressExposesOnlySafeStageAndCounts(t *testing.T) {
	tests := []struct {
		tool  string
		stage string
		read  int32
		edit  int32
		find  int32
		cmd   int32
	}{
		{tool: "Read", stage: progressStageReadingFiles, read: 1},
		{tool: "glob", stage: progressStageReadingFiles, read: 1},
		{tool: "Grep", stage: progressStageSearching, find: 1},
		{tool: "web_search", stage: progressStageSearching, find: 1},
		{tool: "MultiEdit", stage: progressStageEditingFiles, edit: 1},
		{tool: "bash", stage: progressStageRunningCommand, cmd: 1},
		{tool: "unknown_private_tool", stage: progressStageProcessing},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := projectTaskProgress(protocol.TaskMessagePayload{
				Seq:    7,
				Type:   "tool_use",
				Tool:   tt.tool,
				Input:  map[string]any{"command": "rm -rf /secret", "path": "/private/repo"},
				Output: "token=must-not-leak",
			})
			if got.Stage != tt.stage || got.FilesReadDelta != tt.read || got.FilesEditedDelta != tt.edit || got.SearchesDelta != tt.find || got.CommandsDelta != tt.cmd {
				t.Fatalf("projection=%+v", got)
			}
			if got.VisibleTextAppend != "" {
				t.Fatalf("tool details leaked into visible text: %q", got.VisibleTextAppend)
			}
		})
	}
}

func TestProjectTaskProgressKeepsAssistantTextButNeverThinking(t *testing.T) {
	text := projectTaskProgress(protocol.TaskMessagePayload{Seq: 1, Type: "text", Content: "已完成路由设计。"})
	if text.VisibleTextAppend != "已完成路由设计。" || text.Stage != progressStageResponding {
		t.Fatalf("text projection=%+v", text)
	}
	thinking := projectTaskProgress(protocol.TaskMessagePayload{Seq: 2, Type: "thinking", Content: "secret chain of thought"})
	if thinking.VisibleTextAppend != "" || thinking.Stage != progressStageProcessing {
		t.Fatalf("thinking projection=%+v", thinking)
	}
}

func TestDefaultRendererProducesBoundedCardKitStreamingCard(t *testing.T) {
	render, err := NewDefaultRenderer().Render(RenderInput{
		Kind:        CardKindRunning,
		AgentName:   "CodeM",
		Content:     strings.Repeat("中", 40000),
		Stage:       progressStageEditingFiles,
		ElapsedSecs: 17,
		FilesRead:   7,
		FilesEdited: 5,
		Commands:    1,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len([]byte(render.JSON)) > maxStreamingCardBytes {
		t.Fatalf("card bytes=%d want <=%d", len([]byte(render.JSON)), maxStreamingCardBytes)
	}
	for _, want := range []string{`"schema":"2.0"`, `"streaming_mode":true`, `"element_id":"agent_progress"`, "正在修改文件 · 17 秒", "读取 7 个文件", "修改 5 个文件", "执行 1 条命令"} {
		if !strings.Contains(render.JSON, want) {
			t.Fatalf("card missing %q: %s", want, render.JSON)
		}
	}
	for _, forbidden := range []string{"rm -rf", "/private/repo", "secret chain of thought"} {
		if strings.Contains(render.JSON, forbidden) {
			t.Fatalf("card leaked %q", forbidden)
		}
	}
}

func TestDefaultRendererClosesStreamingForTerminalCard(t *testing.T) {
	render, err := NewDefaultRenderer().Render(RenderInput{Kind: CardKindFinal, Content: "完成"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(render.JSON, `"streaming_mode":false`) {
		t.Fatalf("terminal card must close streaming mode: %s", render.JSON)
	}
}

func TestDefaultRendererKeepsLargeTerminalCardTerminal(t *testing.T) {
	render, err := NewDefaultRenderer().Render(RenderInput{
		Kind: CardKindFinal, Content: strings.Repeat("最终答案", 10000),
		Stage: progressStageEditingFiles, FilesEdited: 99,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len([]byte(render.JSON)) > maxStreamingCardBytes {
		t.Fatalf("card bytes=%d want <=%d", len([]byte(render.JSON)), maxStreamingCardBytes)
	}
	for _, forbidden := range []string{"正在修改文件", "修改 99 个文件"} {
		if strings.Contains(render.JSON, forbidden) {
			t.Fatalf("terminal card regressed to progress body: %s", render.JSON)
		}
	}
}
