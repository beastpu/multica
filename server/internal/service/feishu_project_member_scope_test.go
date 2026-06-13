package service

import "testing"

// The assignee scope gate decides whether a Feishu work item should be synced
// into this workspace based on its operator. It is a creation-time gate: an
// item that already has a bound issue keeps syncing regardless of membership
// (reassignment is a documented limitation, not a removal trigger).
func TestFeishuProjectSkipsNonMemberWorkItem(t *testing.T) {
	cases := []struct {
		name          string
		memberOnly    bool
		issueExists   bool
		ownerIsMember bool
		wantSkip      bool
	}{
		{name: "scope off syncs everything", memberOnly: false, issueExists: false, ownerIsMember: false, wantSkip: false},
		{name: "scope off keeps non-member item", memberOnly: false, issueExists: true, ownerIsMember: false, wantSkip: false},
		{name: "member-only new member item syncs", memberOnly: true, issueExists: false, ownerIsMember: true, wantSkip: false},
		{name: "member-only new non-member item skipped", memberOnly: true, issueExists: false, ownerIsMember: false, wantSkip: true},
		{name: "member-only existing item keeps syncing after reassign", memberOnly: true, issueExists: true, ownerIsMember: false, wantSkip: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := feishuProjectSkipsNonMemberWorkItem(tc.memberOnly, tc.issueExists, tc.ownerIsMember)
			if got != tc.wantSkip {
				t.Fatalf("feishuProjectSkipsNonMemberWorkItem(%v,%v,%v) = %v, want %v",
					tc.memberOnly, tc.issueExists, tc.ownerIsMember, got, tc.wantSkip)
			}
		})
	}
}
