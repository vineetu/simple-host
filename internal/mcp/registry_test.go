package mcp

import "testing"

func TestRegistryRejectsDuplicateEditionName(t *testing.T) {
	r := &toolRegistry{}
	r.add(editionHost, famSites, []Tool{{Name: "duplicate"}})
	r.add(editionHack, "fixture", []Tool{{Name: "duplicate"}})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate name within an edition did not panic")
		}
	}()
	r.add(editionHost, famAccess, []Tool{{Name: "duplicate"}})
}

func TestNotificationPolicyLookupKeepsHostSemantics(t *testing.T) {
	s := NewServer(testConfig(Config{Events: inventoryEvents{}}))
	for _, name := range []string{"list_notifications", "remove_notification"} {
		t.Run(name, func(t *testing.T) {
			const email = "person@example.test"
			result := dropAnalyticsValuesForPolicy(s.policy(name), map[string]any{"email": email}).(map[string]any)
			if result["email"] != email {
				t.Fatal("the product's policy overrode Host semantics for a shared name")
			}
			// The product's callable declaration still keeps its own edition policy.
			if !s.productByName[name].policy.analytics.emailIdentity {
				t.Fatal("the product's declaration lost its edition policy")
			}
		})
	}
}
