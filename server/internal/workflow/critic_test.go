package workflow

import "testing"

func TestParseCriticOutput(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		approved bool
		comment  string
		wantErr  bool
	}{
		{name: "approve", output: `{"approved":true,"comment":"meets AC"}`, approved: true, comment: "meets AC"},
		{name: "reject", output: "```json\n{\"approved\":false,\"comment\":\"add the missing test\"}\n```", comment: "add the missing test"},
		{name: "reject needs reason", output: `{"approved":false,"comment":""}`, wantErr: true},
		{name: "approved is required", output: `{"comment":"looks fine"}`, wantErr: true},
		{name: "unknown field", output: `{"approved":true,"comment":"ok","score":1}`, wantErr: true},
		{name: "prose is refused", output: `Approved: {"approved":true,"comment":"ok"}`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseCriticOutput(test.output)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Approved != test.approved || got.Comment != test.comment {
				t.Fatalf("unexpected verdict: %+v", got)
			}
		})
	}
}
