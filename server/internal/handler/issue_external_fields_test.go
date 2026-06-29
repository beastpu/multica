package handler

import (
	"reflect"
	"testing"
)

func TestFeishuProjectExternalFieldsToResponseFiltersInvalidValues(t *testing.T) {
	got := feishuProjectExternalFieldsToResponse([]byte(`{
		"提交分支": "  1.7.2(dev or rel)  ",
		"开发分支": "main, rel_1.1.0",
		"empty": "  ",
		"nested": {"value": "ignored"}
	}`))
	want := map[string]string{
		"提交分支": "1.7.2(dev or rel)",
		"开发分支": "main, rel_1.1.0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("external fields = %#v, want %#v", got, want)
	}
}
