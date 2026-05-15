package handler

import "testing"

func TestValidNotificationPreferenceGroupsIncludeDeliveryChannels(t *testing.T) {
	for _, key := range []string{"system_notifications", "feishu_notifications"} {
		if !validNotifGroups[key] {
			t.Fatalf("expected %q to be an accepted notification preference group", key)
		}
	}
}
