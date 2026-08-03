package workflow

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed builtin_templates/*.json
var builtinTemplateFS embed.FS

// BuiltinWorkflowTemplate is a ready-to-use workflow definition shipped with
// the server. Instantiating one copies its definition into a workspace-owned
// template, so builtins themselves stay immutable across releases.
type BuiltinWorkflowTemplate struct {
	Key         string
	Name        string
	Description string
	Definition  json.RawMessage
}

var builtinTemplates = loadBuiltinTemplates([]struct {
	key         string
	description string
}{
	{
		key: "requirement_delivery",
		description: "需求评审 → 方案设计 → 代码实施的标准需求交付流程，" +
			"方案设计由产品负责人评审，代码实施由验收人准出，" +
			"开发环节可由成员、Agent 或小队执行。",
	},
	{
		key: "bug_fix",
		description: "问题分诊 → 根因分析 → 缺陷修复的缺陷闭环流程，" +
			"修复由测试负责人评审通过后直接完成，" +
			"分析和修复环节都可交给 Agent 自动执行。",
	},
})

func loadBuiltinTemplates(metas []struct {
	key         string
	description string
}) []BuiltinWorkflowTemplate {
	templates := make([]BuiltinWorkflowTemplate, 0, len(metas))
	for _, meta := range metas {
		raw, err := builtinTemplateFS.ReadFile(
			fmt.Sprintf("builtin_templates/%s.json", meta.key),
		)
		if err != nil {
			panic(fmt.Sprintf("builtin workflow template %q: %v", meta.key, err))
		}
		definition, err := ParseDefinition(raw)
		if err != nil {
			panic(fmt.Sprintf("builtin workflow template %q invalid: %v", meta.key, err))
		}
		templates = append(templates, BuiltinWorkflowTemplate{
			Key:         meta.key,
			Name:        definition.Name,
			Description: meta.description,
			Definition:  json.RawMessage(raw),
		})
	}
	return templates
}

// BuiltinTemplates returns the shipped templates in display order.
func BuiltinTemplates() []BuiltinWorkflowTemplate {
	return append([]BuiltinWorkflowTemplate(nil), builtinTemplates...)
}

// FindBuiltinTemplate resolves a builtin template by key.
func FindBuiltinTemplate(key string) (BuiltinWorkflowTemplate, bool) {
	for _, template := range builtinTemplates {
		if template.Key == key {
			return template, true
		}
	}
	return BuiltinWorkflowTemplate{}, false
}
