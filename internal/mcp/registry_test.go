package mcp

import "testing"

func TestRegistryRejectsDuplicateEditionName(t *testing.T) {
	r := &toolRegistry{}
	r.add(editionHost, famSites, []Tool{{Name: "duplicate"}})
	r.add(editionHack, famHackEvents, []Tool{{Name: "duplicate"}})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate name within an edition did not panic")
		}
	}()
	r.add(editionHost, famAccess, []Tool{{Name: "duplicate"}})
}

func TestNotificationPolicyLookupKeepsHostSemantics(t *testing.T) {
	s := NewServer(Config{Events: inventoryEvents{}})
	for _, name := range []string{"list_notifications", "remove_notification"} {
		t.Run(name, func(t *testing.T) {
			const email = "person@example.test"
			result := dropAnalyticsValuesForPolicy(s.policy(name), map[string]any{"email": email}).(map[string]any)
			if result["email"] != email {
				t.Fatal("Hack policy overrode Host semantics for a shared name")
			}
			// The callable Hack declaration still keeps its own edition policy.
			if !s.hackByName[name].policy.analytics.emailIdentity {
				t.Fatal("Hack declaration lost its edition policy")
			}
		})
	}
}
