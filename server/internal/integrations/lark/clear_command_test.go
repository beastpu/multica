package lark

import "testing"

func TestParseClearCommand(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "clear alone", body: "/clear", want: true},
		{name: "clear with whitespace", body: "/clear   ", want: true},
		{name: "leading blanks", body: "\n\n /clear", want: true},
		{name: "arguments tolerated", body: "/clear please", want: true},
		{name: "mid sentence rejected", body: "please /clear", want: false},
		{name: "wrong case rejected", body: "/Clear", want: false},
		{name: "prefix token rejected", body: "/clearer", want: false},
		{name: "empty rejected", body: "", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseClearCommand(tc.body); got != tc.want {
				t.Fatalf("parseClearCommand(%q) = %v want %v", tc.body, got, tc.want)
			}
		})
	}
}
