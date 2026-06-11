package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type FeishuProjectIntegrationResponse struct {
	ID              string `json:"id,omitempty"`
	WorkspaceID     string `json:"workspace_id,omitempty"`
	ProjectName     string `json:"project_name"`
	ProjectKey      string `json:"project_key"`
	PluginID        string `json:"plugin_id"`
	HasPluginSecret bool   `json:"has_plugin_secret"`
	// Deployment-wide company plugin credentials. When available, an
	// integration with an empty plugin_id uses them automatically.
	DefaultPluginAvailable bool    `json:"default_plugin_available"`
	DefaultPluginID        string  `json:"default_plugin_id,omitempty"`
	ActorUserKey           *string `json:"actor_user_key"`
	Enabled                bool    `json:"enabled"`
	SyncStory              bool    `json:"sync_story"`
	// SyncIssue / StatusMapping / ReverseStatusMapping are legacy aliases of
	// the work_item_types issue entry, kept for older desktop clients.
	SyncIssue                   bool                                      `json:"sync_issue"`
	MQLFilter                   string                                    `json:"mql_filter"`
	StatusMapping               map[string]string                         `json:"status_mapping"`
	ReverseStatusMapping        map[string]string                         `json:"reverse_status_mapping"`
	WorkItemTypes               []service.FeishuProjectWorkItemTypeConfig `json:"work_item_types"`
	AssignOpenItemsToOwnerAgent bool                                      `json:"assign_open_items_to_owner_agent"`
	BusinessLineFieldKey        string                                    `json:"business_line_field_key"`
	BusinessLineFieldName       string                                    `json:"business_line_field_name"`
	LabelSyncRules              []service.FeishuProjectLabelSyncRule      `json:"label_sync_rules"`
	LastSyncedAt                *string                                   `json:"last_synced_at"`
	LastError                   *string                                   `json:"last_error"`
	CreatedAt                   string                                    `json:"created_at,omitempty"`
	UpdatedAt                   string                                    `json:"updated_at,omitempty"`
}

type UpdateFeishuProjectIntegrationRequest struct {
	ProjectName  string  `json:"project_name"`
	ProjectKey   string  `json:"project_key"`
	PluginID     string  `json:"plugin_id"`
	PluginSecret *string `json:"plugin_secret"`
	ActorUserKey *string `json:"actor_user_key"`
	Enabled      bool    `json:"enabled"`
	SyncStory    bool    `json:"sync_story"`
	SyncIssue    bool    `json:"sync_issue"`
	MQLFilter    string  `json:"mql_filter"`
	// Legacy flat mappings — older clients that don't know work_item_types
	// still edit the issue mappings through these.
	StatusMapping        map[string]string `json:"status_mapping"`
	ReverseStatusMapping map[string]string `json:"reverse_status_mapping"`
	// WorkItemTypes replaces per-type fields. nil (absent) preserves the
	// stored list (modulo the legacy issue-mapping merge above); non-nil
	// replaces it wholesale.
	WorkItemTypes               *[]service.FeishuProjectWorkItemTypeConfig `json:"work_item_types"`
	AssignOpenItemsToOwnerAgent bool                                       `json:"assign_open_items_to_owner_agent"`
	BusinessLineFieldKey        string                                     `json:"business_line_field_key"`
	BusinessLineFieldName       string                                     `json:"business_line_field_name"`
	LabelSyncRules              *[]service.FeishuProjectLabelSyncRule      `json:"label_sync_rules"`
}

type FeishuProjectSyncRunResponse struct {
	ID          string  `json:"id"`
	Status      string  `json:"status"`
	Trigger     string  `json:"trigger"`
	Created     int32   `json:"created"`
	Updated     int32   `json:"updated"`
	Skipped     int32   `json:"skipped"`
	Errors      int32   `json:"errors"`
	Processed   int32   `json:"processed"`
	Total       int32   `json:"total"`
	CurrentPage int32   `json:"current_page"`
	CurrentType string  `json:"current_type"`
	Error       *string `json:"error"`
	StartedAt   *string `json:"started_at"`
	FinishedAt  *string `json:"finished_at"`
}

type FeishuProjectSyncResponse struct {
	Status  string                           `json:"status"`
	Run     *FeishuProjectSyncRunResponse    `json:"run,omitempty"`
	Summary service.FeishuProjectSyncSummary `json:"summary"`
	Error   string                           `json:"error,omitempty"`
}

type SyncFeishuProjectIntegrationRequest struct {
	WorkItemID string `json:"work_item_id"`
}

func (h *Handler) GetFeishuProjectIntegration(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	cfg, err := h.Queries.GetFeishuProjectIntegration(r.Context(), parseUUID(workspaceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, FeishuProjectIntegrationResponse{
				Enabled:                     false,
				SyncStory:                   false,
				SyncIssue:                   true,
				MQLFilter:                   "",
				StatusMapping:               defaultFeishuProjectStatusMapping(),
				ReverseStatusMapping:        defaultFeishuProjectReverseStatusMapping(),
				WorkItemTypes:               []service.FeishuProjectWorkItemTypeConfig{defaultFeishuProjectIssueTypeConfig()},
				DefaultPluginAvailable:      service.FeishuProjectHasDefaultPluginCredentials(),
				DefaultPluginID:             service.FeishuProjectDefaultPluginID(),
				AssignOpenItemsToOwnerAgent: false,
				LabelSyncRules:              []service.FeishuProjectLabelSyncRule{},
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load Feishu Project integration")
		return
	}
	writeJSON(w, http.StatusOK, feishuProjectIntegrationToResponse(cfg))
}

func (h *Handler) UpdateFeishuProjectIntegration(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin")
	if !ok {
		return
	}
	var req UpdateFeishuProjectIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	projectKey := feishuProjectNameFromRequest(req)
	if projectKey == "" {
		writeError(w, http.StatusBadRequest, "project_name is required")
		return
	}
	pluginID := strings.TrimSpace(req.PluginID)
	pluginSecret := ""
	if req.PluginSecret != nil {
		pluginSecret = strings.TrimSpace(*req.PluginSecret)
	}
	wsUUID := parseUUID(workspaceID)
	existing, existingErr := h.Queries.GetFeishuProjectIntegration(r.Context(), wsUUID)
	if existingErr != nil && !errors.Is(existingErr, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load existing Feishu Project integration")
		return
	}
	// Empty plugin_id means "use the deployment-wide company plugin"; both
	// stored credential fields stay empty and the service resolves them from
	// env at call time. A custom plugin still needs its own secret.
	if pluginID == "" {
		if !service.FeishuProjectHasDefaultPluginCredentials() {
			writeError(w, http.StatusBadRequest, "plugin_id is required (no default plugin configured)")
			return
		}
		pluginSecret = ""
	} else {
		if pluginSecret == "" && existingErr == nil && existing.PluginID == pluginID {
			pluginSecret = existing.PluginSecret
		}
		if pluginSecret == "" {
			writeError(w, http.StatusBadRequest, "plugin_secret is required")
			return
		}
	}

	// Resolve the synced type list: explicit list wins; otherwise keep the
	// stored list and merge the legacy flat issue mappings into its issue
	// entry so older clients editing only those fields stay effective.
	var typeConfigs []service.FeishuProjectWorkItemTypeConfig
	if req.WorkItemTypes != nil {
		var ok bool
		typeConfigs, ok = h.normalizeFeishuProjectWorkItemTypes(w, r, wsUUID, *req.WorkItemTypes)
		if !ok {
			return
		}
	} else {
		if existingErr == nil {
			typeConfigs = service.FeishuProjectWorkItemTypeConfigs(existing)
		}
		typeConfigs = mergeLegacyIssueMappings(typeConfigs, req)
	}
	workItemTypesJSON, _ := json.Marshal(typeConfigs)

	// Legacy flat columns mirror the issue entry so old desktop clients keep
	// seeing a coherent config.
	syncIssue := false
	statusMapping := map[string]string{}
	reverseMapping := map[string]string{}
	for _, entry := range typeConfigs {
		if entry.TypeKey == "issue" {
			syncIssue = true
			if entry.StatusMapping != nil {
				statusMapping = entry.StatusMapping
			}
			if entry.ReverseStatusMapping != nil {
				reverseMapping = entry.ReverseStatusMapping
			}
			break
		}
	}
	statusJSON, _ := json.Marshal(statusMapping)
	reverseJSON, _ := json.Marshal(reverseMapping)
	labelSyncRules := []service.FeishuProjectLabelSyncRule{}
	if req.LabelSyncRules != nil {
		var ok bool
		labelSyncRules, ok = normalizeFeishuProjectLabelSyncRules(w, *req.LabelSyncRules)
		if !ok {
			return
		}
	} else if existingErr == nil {
		labelSyncRules = decodeFeishuProjectLabelSyncRules(existing.LabelSyncRules)
	}
	labelSyncRulesJSON, _ := json.Marshal(labelSyncRules)
	mqlFilter := strings.TrimSpace(req.MQLFilter)
	var actor pgtype.Text
	if req.ActorUserKey != nil && strings.TrimSpace(*req.ActorUserKey) != "" {
		actor = pgtype.Text{String: strings.TrimSpace(*req.ActorUserKey), Valid: true}
	}
	var cfg db.FeishuProjectIntegration
	var err error
	bizLineKey := strings.TrimSpace(req.BusinessLineFieldKey)
	bizLineName := strings.TrimSpace(req.BusinessLineFieldName)
	if existingErr == nil {
		cfg, err = h.Queries.UpdateFeishuProjectIntegrationByID(r.Context(), db.UpdateFeishuProjectIntegrationByIDParams{
			ID:                          existing.ID,
			WorkspaceID:                 wsUUID,
			ProjectKey:                  projectKey,
			PluginID:                    pluginID,
			PluginSecret:                pluginSecret,
			ActorUserKey:                actor,
			Enabled:                     req.Enabled,
			SyncStory:                   req.SyncStory,
			SyncIssue:                   syncIssue,
			MqlFilter:                   mqlFilter,
			StatusMapping:               statusJSON,
			ReverseStatusMapping:        reverseJSON,
			AssignOpenItemsToOwnerAgent: req.AssignOpenItemsToOwnerAgent,
			BusinessLineFieldKey:        bizLineKey,
			BusinessLineFieldName:       bizLineName,
			LabelSyncRules:              labelSyncRulesJSON,
			WorkItemTypes:               workItemTypesJSON,
		})
	} else {
		cfg, err = h.Queries.UpsertFeishuProjectIntegration(r.Context(), db.UpsertFeishuProjectIntegrationParams{
			WorkspaceID:                 wsUUID,
			ProjectKey:                  projectKey,
			PluginID:                    pluginID,
			PluginSecret:                pluginSecret,
			ActorUserKey:                actor,
			Enabled:                     req.Enabled,
			SyncStory:                   req.SyncStory,
			SyncIssue:                   syncIssue,
			MqlFilter:                   mqlFilter,
			StatusMapping:               statusJSON,
			ReverseStatusMapping:        reverseJSON,
			AssignOpenItemsToOwnerAgent: req.AssignOpenItemsToOwnerAgent,
			CreatedByID:                 member.UserID,
			BusinessLineFieldKey:        bizLineKey,
			BusinessLineFieldName:       bizLineName,
			LabelSyncRules:              labelSyncRulesJSON,
			WorkItemTypes:               workItemTypesJSON,
		})
	}
	if err != nil {
		slog.Warn("update Feishu Project integration failed", "workspace_id", workspaceID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update Feishu Project integration")
		return
	}
	writeJSON(w, http.StatusOK, feishuProjectIntegrationToResponse(cfg))
}

// defaultFeishuProjectIssueTypeConfig is the issue entry a fresh integration
// starts with — mirrors the legacy issue-only default behavior.
func defaultFeishuProjectIssueTypeConfig() service.FeishuProjectWorkItemTypeConfig {
	return service.FeishuProjectWorkItemTypeConfig{
		TypeKey:              "issue",
		APIName:              "issue",
		Name:                 "缺陷",
		IdentifierPrefix:     "BUG",
		StatusMapping:        defaultFeishuProjectStatusMapping(),
		ReverseStatusMapping: defaultFeishuProjectReverseStatusMapping(),
	}
}

var feishuProjectIdentifierPrefixRe = regexp.MustCompile(`^[A-Z0-9_]{1,16}$`)

// feishuProjectTypeKeyRe bounds a Meego work-item type_key (hex id like
// 637c83ce54b03d5198e2d1cb, or a snake name like "issue"/"sub_task"). Enforced
// on write AND read so the value is safe to interpolate into Meego OpenAPI URL
// paths — no '/' or '.' can reach fmt.Sprintf("/open_api/%s/.../%s", ...).
var feishuProjectTypeKeyRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// validFeishuProjectWorkItemType normalizes and validates a work_item_type query
// param. Empty → "issue" (legacy default). Returns ok=false for anything that
// could escape the OpenAPI path; caller writes the 400.
func validFeishuProjectWorkItemType(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "issue", true
	}
	if !feishuProjectTypeKeyRe.MatchString(raw) {
		return "", false
	}
	return raw, true
}

// normalizeFeishuProjectWorkItemTypes validates and canonicalizes a client-sent
// type list. Writes the HTTP error itself and returns ok=false on bad input.
func (h *Handler) normalizeFeishuProjectWorkItemTypes(w http.ResponseWriter, r *http.Request, wsUUID pgtype.UUID, in []service.FeishuProjectWorkItemTypeConfig) ([]service.FeishuProjectWorkItemTypeConfig, bool) {
	out := make([]service.FeishuProjectWorkItemTypeConfig, 0, len(in))
	seen := map[string]bool{}
	for i, entry := range in {
		entry.TypeKey = strings.TrimSpace(entry.TypeKey)
		if entry.TypeKey == "" {
			writeError(w, http.StatusBadRequest, "work_item_types["+strconv.Itoa(i)+"].type_key is required")
			return nil, false
		}
		if seen[entry.TypeKey] {
			writeError(w, http.StatusBadRequest, "work_item_types["+strconv.Itoa(i)+"].type_key must be unique")
			return nil, false
		}
		if !feishuProjectTypeKeyRe.MatchString(entry.TypeKey) {
			writeError(w, http.StatusBadRequest, "work_item_types["+strconv.Itoa(i)+"].type_key must match [A-Za-z0-9_]{1,64}")
			return nil, false
		}
		seen[entry.TypeKey] = true
		entry.APIName = strings.TrimSpace(entry.APIName)
		entry.Name = strings.TrimSpace(entry.Name)
		entry.IdentifierPrefix = strings.ToUpper(strings.TrimSpace(entry.IdentifierPrefix))
		if entry.IdentifierPrefix != "" && !feishuProjectIdentifierPrefixRe.MatchString(entry.IdentifierPrefix) {
			writeError(w, http.StatusBadRequest, "work_item_types["+strconv.Itoa(i)+"].identifier_prefix must match [A-Z0-9_]{1,16}")
			return nil, false
		}
		entry.ProjectID = strings.TrimSpace(entry.ProjectID)
		if entry.ProjectID != "" {
			projectID, ok := parseUUIDOrBadRequest(w, entry.ProjectID, "work_item_types["+strconv.Itoa(i)+"].project_id")
			if !ok {
				return nil, false
			}
			if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: wsUUID}); err != nil {
				writeError(w, http.StatusBadRequest, "work_item_types["+strconv.Itoa(i)+"].project_id does not reference a project in this workspace")
				return nil, false
			}
		}
		if entry.StatusMapping == nil {
			entry.StatusMapping = map[string]string{}
		}
		if entry.ReverseStatusMapping == nil {
			entry.ReverseStatusMapping = map[string]string{}
		}
		out = append(out, entry)
	}
	return out, true
}

// mergeLegacyIssueMappings folds the legacy flat issue-mapping fields of an
// old-client PUT into the stored type list, so pre-work_item_types desktop
// builds editing the issue mappings still take effect.
//
// A mapping is applied ONLY when the request explicitly carries it (non-nil):
// a partial PUT that omits status_mapping/reverse_status_mapping must preserve
// the stored issue entry's mappings, never reset them to defaults. The canned
// defaults are used solely when seeding a brand-new issue entry for a legacy
// client that turned issue sync on without sending mappings.
func mergeLegacyIssueMappings(configs []service.FeishuProjectWorkItemTypeConfig, req UpdateFeishuProjectIntegrationRequest) []service.FeishuProjectWorkItemTypeConfig {
	for i := range configs {
		if configs[i].TypeKey == "issue" {
			if req.StatusMapping != nil {
				configs[i].StatusMapping = req.StatusMapping
			}
			if req.ReverseStatusMapping != nil {
				configs[i].ReverseStatusMapping = req.ReverseStatusMapping
			}
			return configs
		}
	}
	if !req.SyncIssue {
		return configs
	}
	// No issue entry yet, but a legacy client wants issue sync on — seed one,
	// preferring the request's mappings and falling back to the defaults.
	entry := defaultFeishuProjectIssueTypeConfig()
	if req.StatusMapping != nil {
		entry.StatusMapping = req.StatusMapping
	}
	if req.ReverseStatusMapping != nil {
		entry.ReverseStatusMapping = req.ReverseStatusMapping
	}
	return append([]service.FeishuProjectWorkItemTypeConfig{entry}, configs...)
}

func normalizeFeishuProjectLabelSyncRules(w http.ResponseWriter, in []service.FeishuProjectLabelSyncRule) ([]service.FeishuProjectLabelSyncRule, bool) {
	out := make([]service.FeishuProjectLabelSyncRule, 0, len(in))
	seen := map[string]bool{}
	for i, rule := range in {
		id := strings.TrimSpace(rule.ID)
		if id == "" {
			writeError(w, http.StatusBadRequest, "label_sync_rules["+strconv.Itoa(i)+"].id is required")
			return nil, false
		}
		if seen[id] {
			writeError(w, http.StatusBadRequest, "label_sync_rules["+strconv.Itoa(i)+"].id must be unique")
			return nil, false
		}
		seen[id] = true
		fieldKey := strings.TrimSpace(rule.FieldKey)
		match := strings.TrimSpace(rule.Match)
		labelName, err := validateLabelName(rule.LabelName)
		if err != nil {
			writeError(w, http.StatusBadRequest, "label_sync_rules["+strconv.Itoa(i)+"].label_name is invalid")
			return nil, false
		}
		if fieldKey == "" || match == "" {
			writeError(w, http.StatusBadRequest, "label_sync_rules["+strconv.Itoa(i)+"].field_key and match are required")
			return nil, false
		}
		out = append(out, service.FeishuProjectLabelSyncRule{
			ID:        id,
			Enabled:   rule.Enabled,
			FieldKey:  fieldKey,
			FieldName: strings.TrimSpace(rule.FieldName),
			Match:     match,
			LabelName: labelName,
		})
	}
	return out, true
}

func (h *Handler) SyncFeishuProjectIntegration(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	cfg, err := h.Queries.GetFeishuProjectIntegration(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusNotFound, "Feishu Project integration not found")
		return
	}
	var req SyncFeishuProjectIntegrationRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	req.WorkItemID = strings.TrimSpace(req.WorkItemID)
	if latest, err := h.Queries.GetLatestFeishuProjectManualSyncRun(r.Context(), cfg.ID); err == nil && latest.Status == "running" {
		if latest.StartedAt.Valid && time.Since(latest.StartedAt.Time) > 2*time.Hour {
			_ = h.Queries.FinishFeishuProjectSyncRun(r.Context(), db.FinishFeishuProjectSyncRunParams{
				ID:           latest.ID,
				Status:       "failed",
				CreatedCount: latest.CreatedCount,
				UpdatedCount: latest.UpdatedCount,
				SkippedCount: latest.SkippedCount,
				ErrorCount:   latest.ErrorCount + 1,
				Error:        pgtype.Text{String: "previous manual sync timed out", Valid: true},
			})
		} else {
			writeJSON(w, http.StatusAccepted, FeishuProjectSyncResponse{Status: "running", Run: feishuProjectSyncRunToResponse(latest)})
			return
		}
	}
	// Manual sync must take the same advisory lock as the scheduled worker.
	// Without it, manual+scheduled can run concurrently against the same
	// integration, which races on create-issue for new work items and
	// double-inserts attachments. h.TxStarter is wired with the pgxpool.Pool
	// at server startup; the type assertion documents that contract.
	locker, ok := h.TxStarter.(*pgxpool.Pool)
	if !ok {
		writeError(w, http.StatusInternalServerError, "sync locker unavailable")
		return
	}
	locked, unlock, err := service.TryAcquireFeishuProjectSyncLock(r.Context(), locker, cfg.ID)
	if err != nil {
		slog.Warn("Feishu Project sync lock acquire failed", "workspace_id", workspaceID, "integration_id", uuidToString(cfg.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to acquire sync lock")
		return
	}
	if !locked {
		// A scheduled run or another manual run holds the lock. Surface the
		// latest run so the UI keeps polling progress instead of starting a
		// duplicate.
		latest, _ := h.Queries.GetLatestFeishuProjectSyncRun(r.Context(), cfg.ID)
		writeJSON(w, http.StatusAccepted, FeishuProjectSyncResponse{Status: "running", Run: feishuProjectSyncRunToResponse(latest)})
		return
	}
	run, err := h.Queries.CreateFeishuProjectSyncRun(r.Context(), db.CreateFeishuProjectSyncRunParams{
		IntegrationID: cfg.ID,
		WorkspaceID:   cfg.WorkspaceID,
		Status:        "running",
		Trigger:       "manual",
	})
	if err != nil {
		unlock()
		writeError(w, http.StatusInternalServerError, "failed to start Feishu Project sync")
		return
	}
	go func() {
		defer unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		svc := &service.FeishuProjectSyncService{Queries: h.Queries, Tx: h.TxStarter, Client: service.NewFeishuProjectClient(), Storage: h.Storage, TaskService: h.TaskService}
		if _, err := svc.SyncWithRunAndOptions(ctx, cfg, "manual", run, service.FeishuProjectSyncOptions{WorkItemID: req.WorkItemID}); err != nil {
			slog.Warn("Feishu Project manual sync failed", "workspace_id", workspaceID, "integration_id", uuidToString(cfg.ID), "run_id", uuidToString(run.ID), "error", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, FeishuProjectSyncResponse{Status: "running", Run: feishuProjectSyncRunToResponse(run)})
}

func (h *Handler) GetFeishuProjectSyncRun(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	cfg, err := h.Queries.GetFeishuProjectIntegration(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusNotFound, "Feishu Project integration not found")
		return
	}
	run, err := h.Queries.GetLatestFeishuProjectSyncRun(r.Context(), cfg.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, FeishuProjectSyncResponse{Status: "idle"})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get Feishu Project sync")
		return
	}
	writeJSON(w, http.StatusOK, FeishuProjectSyncResponse{Status: run.Status, Run: feishuProjectSyncRunToResponse(run), Summary: service.FeishuProjectSyncSummary{
		Created: int(run.CreatedCount),
		Updated: int(run.UpdatedCount),
		Skipped: int(run.SkippedCount),
		Errors:  int(run.ErrorCount),
	}})
}

func (h *Handler) GetFeishuProjectIssueStatuses(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	cfg, err := h.Queries.GetFeishuProjectIntegration(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusNotFound, "Feishu Project integration not found")
		return
	}
	// Validate before it reaches the Meego OpenAPI URL path. Empty → "issue".
	workItemType, ok := validFeishuProjectWorkItemType(r.URL.Query().Get("work_item_type"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid work_item_type")
		return
	}
	statuses, err := service.NewFeishuProjectClient().WorkItemStatusOptions(r.Context(), cfg, workItemType)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"statuses": statuses})
}

// ListFeishuProjectWorkItemTypes returns the Meego space's work-item types so
// the settings UI can offer a picker for the ticket (工单) type — custom type
// keys are space-specific and cannot be hardcoded client-side.
func (h *Handler) ListFeishuProjectWorkItemTypes(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	cfg, err := h.Queries.GetFeishuProjectIntegration(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusNotFound, "Feishu Project integration not found")
		return
	}
	types, err := service.NewFeishuProjectClient().ListWorkItemTypes(r.Context(), cfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if types == nil {
		types = []service.FeishuProjectWorkItemType{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"work_item_types": types})
}

func feishuProjectIntegrationToResponse(cfg db.FeishuProjectIntegration) FeishuProjectIntegrationResponse {
	return FeishuProjectIntegrationResponse{
		ID:                          uuidToString(cfg.ID),
		WorkspaceID:                 uuidToString(cfg.WorkspaceID),
		ProjectName:                 cfg.ProjectKey,
		ProjectKey:                  cfg.ProjectKey,
		PluginID:                    cfg.PluginID,
		HasPluginSecret:             cfg.PluginSecret != "",
		ActorUserKey:                textToPtr(cfg.ActorUserKey),
		Enabled:                     cfg.Enabled,
		SyncStory:                   cfg.SyncStory,
		SyncIssue:                   cfg.SyncIssue,
		MQLFilter:                   cfg.MqlFilter,
		StatusMapping:               decodeFlatStringMap(cfg.StatusMapping),
		ReverseStatusMapping:        decodeFlatStringMap(cfg.ReverseStatusMapping),
		WorkItemTypes:               feishuProjectWorkItemTypesForResponse(cfg),
		DefaultPluginAvailable:      service.FeishuProjectHasDefaultPluginCredentials(),
		DefaultPluginID:             service.FeishuProjectDefaultPluginID(),
		AssignOpenItemsToOwnerAgent: cfg.AssignOpenItemsToOwnerAgent,
		BusinessLineFieldKey:        cfg.BusinessLineFieldKey,
		BusinessLineFieldName:       cfg.BusinessLineFieldName,
		LabelSyncRules:              decodeFeishuProjectLabelSyncRules(cfg.LabelSyncRules),
		LastSyncedAt:                timestampToPtr(cfg.LastSyncedAt),
		LastError:                   textToPtr(cfg.LastError),
		CreatedAt:                   timestampToString(cfg.CreatedAt),
		UpdatedAt:                   timestampToString(cfg.UpdatedAt),
	}
}

// feishuProjectWorkItemTypesForResponse decodes the stored type list with
// JSON-friendly defaults (non-nil slice and maps) for the config response.
func feishuProjectWorkItemTypesForResponse(cfg db.FeishuProjectIntegration) []service.FeishuProjectWorkItemTypeConfig {
	entries := service.FeishuProjectWorkItemTypeConfigs(cfg)
	if entries == nil {
		entries = []service.FeishuProjectWorkItemTypeConfig{}
	}
	for i := range entries {
		if entries[i].StatusMapping == nil {
			entries[i].StatusMapping = map[string]string{}
		}
		if entries[i].ReverseStatusMapping == nil {
			entries[i].ReverseStatusMapping = map[string]string{}
		}
	}
	return entries
}

func decodeFeishuProjectLabelSyncRules(raw []byte) []service.FeishuProjectLabelSyncRule {
	if len(raw) == 0 {
		return []service.FeishuProjectLabelSyncRule{}
	}
	var out []service.FeishuProjectLabelSyncRule
	if err := json.Unmarshal(raw, &out); err != nil {
		return []service.FeishuProjectLabelSyncRule{}
	}
	return out
}

func feishuProjectNameFromRequest(req UpdateFeishuProjectIntegrationRequest) string {
	projectName := strings.TrimSpace(req.ProjectName)
	if projectName != "" {
		return projectName
	}
	return strings.TrimSpace(req.ProjectKey)
}

func feishuProjectSyncRunToResponse(run db.FeishuProjectSyncRun) *FeishuProjectSyncRunResponse {
	return &FeishuProjectSyncRunResponse{
		ID:          uuidToString(run.ID),
		Status:      run.Status,
		Trigger:     run.Trigger,
		Created:     run.CreatedCount,
		Updated:     run.UpdatedCount,
		Skipped:     run.SkippedCount,
		Errors:      run.ErrorCount,
		Processed:   run.ProcessedCount,
		Total:       run.TotalCount,
		CurrentPage: run.CurrentPage,
		CurrentType: run.CurrentType,
		Error:       textToPtr(run.Error),
		StartedAt:   timestampToPtr(run.StartedAt),
		FinishedAt:  timestampToPtr(run.FinishedAt),
	}
}

func decodeFlatStringMap(raw []byte) map[string]string {
	out := map[string]string{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func defaultFeishuProjectStatusMapping() map[string]string {
	return map[string]string{
		"OPEN":        "todo",
		"CLOSED":      "done",
		"REOPENED":    "todo",
		"IN PROGRESS": "in_progress",
		"RESOLVED":    "in_review",
		"新建":          "todo",
		"未开始":         "todo",
		"重新打开":        "todo",
		"处理中":         "in_progress",
		"待测试":         "in_review",
		"待验证":         "in_review",
		"测试通过":        "done",
		"已关闭":         "done",
		"Closed":      "done",
		"挂起":          "blocked",
		"外部原因":        "blocked",
		"暂不处理":        "backlog",
		"无法复现":        "cancelled",
		"设计如此":        "cancelled",
		"放弃":          "cancelled",
		"重复bug":       "cancelled",
		"已终止":         "cancelled",
	}
}

func defaultFeishuProjectReverseStatusMapping() map[string]string {
	return map[string]string{
		"todo":        "OPEN",
		"in_progress": "IN PROGRESS",
		"in_review":   "RESOLVED",
		"done":        "CLOSED",
		"blocked":     "挂起",
		"backlog":     "暂不处理",
		"cancelled":   "已终止",
	}
}

func (h *Handler) DeleteFeishuProjectIntegration(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	cfg, err := h.Queries.GetFeishuProjectIntegration(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if err := h.Queries.DeleteFeishuProjectIntegration(r.Context(), db.DeleteFeishuProjectIntegrationParams{ID: cfg.ID, WorkspaceID: cfg.WorkspaceID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete Feishu Project integration")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
