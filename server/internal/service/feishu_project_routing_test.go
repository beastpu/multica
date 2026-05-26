package service

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// helper: pgtype.UUID with arbitrary bytes for equality assertions.
func uuidLike(seed byte) pgtype.UUID {
	var u pgtype.UUID
	for i := range u.Bytes {
		u.Bytes[i] = seed + byte(i)
	}
	u.Valid = true
	return u
}

func TestExtractBusinessLineTokensSingleObject(t *testing.T) {
	got := extractBusinessLineTokens(map[string]any{
		"option_id":          "child-id",
		"option_name":        "活动中心-Event",
		"parent_option_id":   "parent-id",
		"parent_option_name": "玩家服务组",
	})
	want := []FeishuBusinessLineToken{{
		ID: "child-id", Name: "活动中心-Event",
		ParentID: "parent-id", ParentName: "玩家服务组",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestExtractBusinessLineTokensArray(t *testing.T) {
	got := extractBusinessLineTokens([]any{
		map[string]any{"id": "a-id", "name": "A"},
		map[string]any{"id": "b-id", "name": "B"},
	})
	if len(got) != 2 || got[0].ID != "a-id" || got[1].Name != "B" {
		t.Fatalf("got %#v", got)
	}
}

func TestExtractBusinessLineTokensPrimitiveString(t *testing.T) {
	got := extractBusinessLineTokens("opt-1")
	if len(got) != 1 || got[0].ID != "opt-1" || got[0].Name != "" {
		t.Fatalf("got %#v", got)
	}
}

func TestExtractBusinessLineTokensNilEmpty(t *testing.T) {
	if got := extractBusinessLineTokens(nil); got != nil {
		t.Fatalf("nil → %#v", got)
	}
	if got := extractBusinessLineTokens(map[string]any{}); got != nil {
		t.Fatalf("empty map → %#v", got)
	}
}

func TestMatchBusinessLineRouteLeafIDWins(t *testing.T) {
	leafProj := uuidLike(1)
	parentProj := uuidLike(2)
	routes := []db.FeishuProjectBusinessLineRoute{
		{ProjectID: parentProj, BusinessLineID: "parent-id", BusinessLineName: "Parent"},
		{ProjectID: leafProj, BusinessLineID: "leaf-id", BusinessLineName: "Leaf"},
	}
	tokens := []FeishuBusinessLineToken{
		{ID: "leaf-id", Name: "Leaf", ParentID: "parent-id", ParentName: "Parent"},
	}
	got := matchBusinessLineRoute(routes, tokens)
	if got == nil || got.ProjectID != leafProj {
		t.Fatalf("expected leaf route to win, got %#v", got)
	}
}

func TestMatchBusinessLineRouteParentRollup(t *testing.T) {
	parentProj := uuidLike(3)
	routes := []db.FeishuProjectBusinessLineRoute{
		{ProjectID: parentProj, BusinessLineID: "parent-id", BusinessLineName: "Parent"},
	}
	tokens := []FeishuBusinessLineToken{
		{ID: "child-not-mapped", Name: "Child", ParentID: "parent-id", ParentName: "Parent"},
	}
	got := matchBusinessLineRoute(routes, tokens)
	if got == nil || got.ProjectID != parentProj {
		t.Fatalf("parent rollup expected, got %#v", got)
	}
}

func TestMatchBusinessLineRouteNameFallback(t *testing.T) {
	proj := uuidLike(4)
	routes := []db.FeishuProjectBusinessLineRoute{
		{ProjectID: proj, BusinessLineID: "stored-id", BusinessLineName: "Leaf Name"},
	}
	// Token only carries name, no id (Meego sometimes returns just labels).
	tokens := []FeishuBusinessLineToken{{Name: "Leaf Name"}}
	got := matchBusinessLineRoute(routes, tokens)
	if got == nil || got.ProjectID != proj {
		t.Fatalf("name fallback expected, got %#v", got)
	}
}

func TestMatchBusinessLineRouteNoMatch(t *testing.T) {
	routes := []db.FeishuProjectBusinessLineRoute{
		{ProjectID: uuidLike(5), BusinessLineID: "unrelated", BusinessLineName: "Other"},
	}
	tokens := []FeishuBusinessLineToken{{ID: "x", Name: "X", ParentID: "y", ParentName: "Y"}}
	if got := matchBusinessLineRoute(routes, tokens); got != nil {
		t.Fatalf("expected no match, got %#v", got)
	}
}

func TestMatchBusinessLineRouteEmptyInputs(t *testing.T) {
	if got := matchBusinessLineRoute(nil, []FeishuBusinessLineToken{{ID: "x"}}); got != nil {
		t.Fatalf("nil routes → %#v", got)
	}
	if got := matchBusinessLineRoute([]db.FeishuProjectBusinessLineRoute{{BusinessLineID: "a"}}, nil); got != nil {
		t.Fatalf("nil tokens → %#v", got)
	}
}

func TestParseFeishuProjectSearchExtractsBusinessLine(t *testing.T) {
	// Meego search response embeds the biz-line value inside `fields[i].field_value`,
	// keyed by `field_key`. Verify that with a configured field key we pull it out;
	// without, we leave BusinessLineTokens nil so legacy 1:1 sync still skips routing.
	payload := map[string]any{
		"data": []any{
			map[string]any{
				"id":   123,
				"name": "test-item",
				"fields": []any{
					map[string]any{
						"field_key": "business",
						"field_value": map[string]any{
							"option_id":          "leaf-id",
							"option_name":        "活动中心-Event",
							"parent_option_id":   "parent-id",
							"parent_option_name": "玩家服务组",
						},
					},
				},
			},
		},
	}
	items := parseFeishuProjectSearch(payload, "issue", "proj", "business")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if len(items[0].BusinessLineTokens) != 1 {
		t.Fatalf("expected 1 token, got %#v", items[0].BusinessLineTokens)
	}
	tok := items[0].BusinessLineTokens[0]
	if tok.ID != "leaf-id" || tok.Name != "活动中心-Event" || tok.ParentID != "parent-id" {
		t.Fatalf("unexpected token: %#v", tok)
	}

	// Without configured field key, no tokens are extracted (1:1 legacy mode).
	items = parseFeishuProjectSearch(payload, "issue", "proj", "")
	if len(items) != 1 || len(items[0].BusinessLineTokens) != 0 {
		t.Fatalf("legacy mode should have no tokens, got %#v", items[0].BusinessLineTokens)
	}
}

func TestParseFeishuProjectBusinessLineTree(t *testing.T) {
	// Shape borrowed from the postman /business/all sample variants — nested children.
	payload := map[string]any{
		"data": []any{
			map[string]any{
				"id": "parent-1", "name": "玩家服务组",
				"children": []any{
					map[string]any{"id": "leaf-a", "name": "网页"},
					map[string]any{"id": "leaf-b", "name": "活动中心-Event"},
				},
			},
			map[string]any{"id": "parent-2", "name": "内部产品组"},
		},
	}
	tree := parseFeishuProjectBusinessLineTree(payload)
	if len(tree) != 2 {
		t.Fatalf("expected 2 roots, got %d (%#v)", len(tree), tree)
	}
	if tree[0].ID != "parent-1" || len(tree[0].Children) != 2 {
		t.Fatalf("unexpected root[0]: %#v", tree[0])
	}
	if tree[0].Children[0].ParentID != "parent-1" || tree[0].Children[0].ParentName != "玩家服务组" {
		t.Fatalf("child parent fields not propagated: %#v", tree[0].Children[0])
	}
}

func TestParseFeishuProjectFieldMetasDedupes(t *testing.T) {
	payload := map[string]any{
		"data": []any{
			map[string]any{"field_key": "business", "name": "业务线", "field_type_key": "_select"},
			map[string]any{"field_key": "owner", "name": "负责人"},
			// duplicate — should be skipped
			map[string]any{"field_key": "business", "name": "业务线 dup"},
		},
	}
	fields := parseFeishuProjectFieldMetas(payload)
	if len(fields) != 2 {
		t.Fatalf("expected 2 unique fields, got %d (%#v)", len(fields), fields)
	}
	// Order isn't important for the assertion below — just that both keys appear once.
	seen := map[string]string{}
	for _, f := range fields {
		seen[f.Key] = f.Name
	}
	if seen["business"] != "业务线" || seen["owner"] != "负责人" {
		t.Fatalf("unexpected fields: %#v", seen)
	}
}
