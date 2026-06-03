package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// orphanProbeServer fakes Feishu Project: it returns only the work items in
// `alive` from each /work_item/filter probe, dropping any requested id that is
// "deleted" — exactly the silent-omission behavior verified against the real
// API. failBatch, when true, makes every probe return a server error.
func orphanProbeServer(t *testing.T, alive map[string]bool, failBatch bool) (*httptest.Server, *[]int) {
	t.Helper()
	batchSizes := []int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open_api/authen/plugin_token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"err_code":0,"data":{"plugin_token":"plugin-token"}}`))
		case "/open_api/project-key/work_item/filter":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode filter request: %v", err)
			}
			ids, _ := req["work_item_ids"].([]any)
			batchSizes = append(batchSizes, len(ids))
			if failBatch {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"err_code":500,"err_msg":"boom"}`))
				return
			}
			items := []string{}
			for _, idAny := range ids {
				id, _ := idAny.(string)
				if alive[id] {
					items = append(items, `{"id": `+id+`, "name": "issue `+id+`", "updated_at": 1778933232000}`)
				}
			}
			body := `{"err_code":0,"data":[` + join(items) + `]}`
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	return server, &batchSizes
}

func join(items []string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += ","
		}
		out += it
	}
	return out
}

func orphanTestService(server *httptest.Server) *FeishuProjectSyncService {
	return &FeishuProjectSyncService{
		Client: &FeishuProjectClient{HTTPClient: server.Client(), BaseURL: server.URL},
	}
}

func orphanCfg() db.FeishuProjectIntegration {
	return db.FeishuProjectIntegration{ProjectKey: "project-key", PluginID: "plugin-id", PluginSecret: "plugin-secret"}
}

func bindingsForIDs(ids ...string) []db.FeishuProjectIssueBinding {
	out := make([]db.FeishuProjectIssueBinding, len(ids))
	for i, id := range ids {
		out[i] = db.FeishuProjectIssueBinding{WorkItemType: "issue", WorkItemID: id}
	}
	return out
}

func TestDetectOrphanBindingsFlagsMissingWorkItems(t *testing.T) {
	server, _ := orphanProbeServer(t, map[string]bool{"1001": true, "1003": true}, false)
	defer server.Close()
	svc := orphanTestService(server)

	orphans, err := svc.detectOrphanBindings(context.Background(), orphanCfg(), bindingsForIDs("1001", "1002", "1003"))
	if err != nil {
		t.Fatalf("detectOrphanBindings: %v", err)
	}
	if len(orphans) != 1 || orphans[0].WorkItemID != "1002" {
		t.Fatalf("orphans = %+v, want only 1002", orphans)
	}
}

func TestDetectOrphanBindingsTreatsProbeFailureAsNoOrphans(t *testing.T) {
	server, _ := orphanProbeServer(t, nil, true)
	defer server.Close()
	svc := orphanTestService(server)

	orphans, err := svc.detectOrphanBindings(context.Background(), orphanCfg(), bindingsForIDs("1001", "1002"))
	if err == nil {
		t.Fatal("expected error on probe failure, got nil")
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %+v, want none on failure", orphans)
	}
}

func TestDetectOrphanBindingsEmptyIsNoop(t *testing.T) {
	server, batches := orphanProbeServer(t, nil, false)
	defer server.Close()
	svc := orphanTestService(server)

	orphans, err := svc.detectOrphanBindings(context.Background(), orphanCfg(), nil)
	if err != nil {
		t.Fatalf("detectOrphanBindings: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %+v, want none", orphans)
	}
	if len(*batches) != 0 {
		t.Fatalf("made %d probes for empty input, want 0", len(*batches))
	}
}

func TestDetectOrphanBindingsChunksAtFifty(t *testing.T) {
	alive := map[string]bool{}
	ids := make([]string, 0, 120)
	for i := 0; i < 120; i++ {
		id := strconv.Itoa(2000 + i)
		ids = append(ids, id)
		alive[id] = true // all alive → expect zero orphans, just exercise batching
	}
	server, batches := orphanProbeServer(t, alive, false)
	defer server.Close()
	svc := orphanTestService(server)

	orphans, err := svc.detectOrphanBindings(context.Background(), orphanCfg(), bindingsForIDs(ids...))
	if err != nil {
		t.Fatalf("detectOrphanBindings: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %d, want 0", len(orphans))
	}
	want := []int{50, 50, 20}
	if len(*batches) != len(want) {
		t.Fatalf("probe batch sizes = %v, want %v", *batches, want)
	}
	for i, n := range want {
		if (*batches)[i] != n {
			t.Fatalf("probe batch sizes = %v, want %v", *batches, want)
		}
	}
}
