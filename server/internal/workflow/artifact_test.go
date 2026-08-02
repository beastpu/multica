package workflow

import "testing"

func definitionWithArtifacts(artifacts string) []byte {
	return []byte(`{
	  "schema_version": 1,
	  "name": "t",
	  "applies_to": {"kind": "issue"},
	  "roles": [{"key": "owner", "name": "Owner", "allowed_actor_types": ["member"]}],
	  "nodes": [
	    {"key":"start","kind":"start","name":"Start"},
	    {"key":"design","kind":"activity","name":"Design",
	     "owner_role":"owner","issue_policy":"none",
	     "completion":{"mode":"manual","required_issue_outcome":"none"},
	     "artifacts": ` + artifacts + `},
	    {"key":"end","kind":"end","name":"End"}
	  ],
	  "edges": [{"from":"start","to":"design"},{"from":"design","to":"end"}],
	  "acceptance": {"policy":"none"}
	}`)
}

func TestArtifactRequirementValidation(t *testing.T) {
	tests := []struct {
		name      string
		artifacts string
		wantErr   bool
	}{
		{
			name:      "a document requirement is accepted",
			artifacts: `[{"key":"design_doc","name":"Technical design","required":true}]`,
		},
		{
			name:      "every declared kind is accepted",
			artifacts: `[{"key":"a","name":"A","kind":"document"},{"key":"b","name":"B","kind":"attachment"},{"key":"c","name":"C","kind":"link"}]`,
		},
		{
			name:      "an unknown kind is rejected",
			artifacts: `[{"key":"a","name":"A","kind":"spreadsheet"}]`,
			wantErr:   true,
		},
		{
			name:      "a duplicate key is rejected",
			artifacts: `[{"key":"a","name":"A"},{"key":"a","name":"Another A"}]`,
			wantErr:   true,
		},
		{
			name:      "a missing name is rejected",
			artifacts: `[{"key":"a","name":"   "}]`,
			wantErr:   true,
		},
		{
			name:      "an invalid key is rejected",
			artifacts: `[{"key":"not a key","name":"A"}]`,
			wantErr:   true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition, err := ParseDefinition(definitionWithArtifacts(test.artifacts))
			if err != nil {
				if !test.wantErr {
					t.Fatalf("ParseDefinition() error = %v", err)
				}
				return
			}
			err = ValidateDefinition(definition)
			if test.wantErr && err == nil {
				t.Fatal("ValidateDefinition() = nil, want an error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("ValidateDefinition() error = %v", err)
			}
		})
	}
}

// Artifacts are delivered by activities. A gateway or join has no executor to
// hold responsible, so allowing one to declare an output would create a
// requirement nobody can satisfy and the node would never complete.
func TestArtifactsRejectedOutsideActivities(t *testing.T) {
	raw := []byte(`{
	  "schema_version": 1,
	  "name": "t",
	  "applies_to": {"kind": "issue"},
	  "roles": [],
	  "nodes": [
	    {"key":"start","kind":"start","name":"Start"},
	    {"key":"gate","kind":"gateway","name":"Gate",
	     "artifacts":[{"key":"report","name":"Report","required":true}]},
	    {"key":"end","kind":"end","name":"End"},
	    {"key":"other","kind":"end","name":"Other end"}
	  ],
	  "edges": [
	    {"from":"start","to":"gate"},
	    {"from":"gate","to":"end","default":true},
	    {"from":"gate","to":"other","condition":{"source":"host_issue","key":"status","op":"eq","value":"done"}}
	  ],
	  "acceptance": {"policy":"none"}
	}`)
	definition, err := ParseDefinition(raw)
	if err != nil {
		return // Rejected while parsing, which is the outcome under test.
	}
	if err := ValidateDefinition(definition); err == nil {
		t.Fatal("a gateway declaring artifacts was accepted, want a rejection")
	}
}

func TestArtifactKindDefaultsToDocument(t *testing.T) {
	if got := ArtifactKind(ArtifactRequirement{Key: "a", Name: "A"}); got != "document" {
		t.Errorf("ArtifactKind() = %q, want %q", got, "document")
	}
	if got := ArtifactKind(ArtifactRequirement{Key: "a", Name: "A", Kind: "link"}); got != "link" {
		t.Errorf("ArtifactKind() = %q, want %q", got, "link")
	}
}

func TestRequiredArtifactsFiltersOptionalOnes(t *testing.T) {
	node := NodeDefinition{Artifacts: []ArtifactRequirement{
		{Key: "design", Name: "Design", Required: true},
		{Key: "notes", Name: "Notes"},
		{Key: "report", Name: "Report", Required: true},
	}}
	required := RequiredArtifacts(node)
	if len(required) != 2 {
		t.Fatalf("RequiredArtifacts() returned %d items, want 2", len(required))
	}
	for _, requirement := range required {
		if !requirement.Required {
			t.Errorf("RequiredArtifacts() returned optional requirement %q", requirement.Key)
		}
	}
}

func definitionWithReviewer(reviewer string) []byte {
	return []byte(`{
	  "schema_version": 1,
	  "name": "t",
	  "applies_to": {"kind": "issue"},
	  "roles": [{"key": "owner", "name": "Owner", "allowed_actor_types": ["member"]}],
	  "nodes": [
	    {"key":"start","kind":"start","name":"Start"},
	    {"key":"check","kind":"activity","name":"Check",
	     "owner_role":"owner","issue_policy":"none",
	     "completion":{"mode":"manual","required_issue_outcome":"none"},
	     "reviewer": ` + reviewer + `},
	    {"key":"end","kind":"end","name":"End"}
	  ],
	  "edges": [{"from":"start","to":"check"},{"from":"check","to":"end"}],
	  "acceptance": {"policy":"none"}
	}`)
}

// An api reviewer is what replaces the objective signal lost when user-defined
// fields go away, so its address has to be trustworthy: https only, and only
// ever from the template.
func TestAPIReviewerValidation(t *testing.T) {
	tests := []struct {
		name     string
		reviewer string
		wantErr  bool
	}{
		{
			name:     "https endpoint is accepted",
			reviewer: `{"kind":"api","required":true,"api_url":"https://ci.example.com/verdict"}`,
		},
		{
			name:     "plaintext http is rejected",
			reviewer: `{"kind":"api","required":true,"api_url":"http://ci.example.com/verdict"}`,
			wantErr:  true,
		},
		{
			name:     "api without a url is rejected",
			reviewer: `{"kind":"api","required":true}`,
			wantErr:  true,
		},
		{
			name:     "a non-api evaluator may not carry a url",
			reviewer: `{"kind":"role","role":"owner","required":true,"api_url":"https://ci.example.com"}`,
			wantErr:  true,
		},
		{
			name:     "member evaluator still works",
			reviewer: `{"kind":"role","role":"owner","required":true}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition, err := ParseDefinition(definitionWithReviewer(test.reviewer))
			if err != nil {
				if !test.wantErr {
					t.Fatalf("ParseDefinition() error = %v", err)
				}
				return
			}
			err = ValidateDefinition(definition)
			if test.wantErr && err == nil {
				t.Fatal("ValidateDefinition() = nil, want an error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("ValidateDefinition() error = %v", err)
			}
		})
	}
}

// node_choice replaces branching on a user-defined field. Its range comes from
// the graph, so validation only has to know the node exists.
func TestNodeChoiceConditionSource(t *testing.T) {
	raw := []byte(`{
	  "schema_version": 1,
	  "name": "t",
	  "applies_to": {"kind": "issue"},
	  "roles": [{"key": "owner", "name": "Owner", "allowed_actor_types": ["member"]}],
	  "nodes": [
	    {"key":"start","kind":"start","name":"Start"},
	    {"key":"triage","kind":"activity","name":"Triage",
	     "owner_role":"owner","issue_policy":"none",
	     "completion":{"mode":"manual","required_issue_outcome":"none"}},
	    {"key":"gate","kind":"gateway","name":"Gate"},
	    {"key":"fix","kind":"end","name":"Fix"},
	    {"key":"reject","kind":"end","name":"Reject"}
	  ],
	  "edges": [
	    {"from":"start","to":"triage"},
	    {"from":"triage","to":"gate"},
	    {"from":"gate","to":"fix","condition":{"source":"node_choice","node":"triage","key":"choice","op":"eq","value":"fix"}},
	    {"from":"gate","to":"reject","default":true}
	  ],
	  "acceptance": {"policy":"none"}
	}`)
	definition, err := ParseDefinition(raw)
	if err != nil {
		t.Fatalf("ParseDefinition() error = %v", err)
	}
	if err := ValidateDefinition(definition); err != nil {
		t.Fatalf("ValidateDefinition() error = %v", err)
	}
}
