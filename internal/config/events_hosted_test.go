package config

import "testing"

func TestEventsHosted(t *testing.T) {
	for _, value := range []string{"hosted", " HOSTED\t", "off", "", "unknown"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("EVENTS", value)
			want := value == "hosted" || value == " HOSTED\t"
			if got := EventsHosted(); got != want {
				t.Fatalf("EventsHosted() = %v, want %v", got, want)
			}
			var cfg Config
			if err := loadEvents(&cfg); value == "unknown" {
				if err == nil {
					t.Fatal("invalid EVENTS setting accepted")
				}
			} else if err != nil || (cfg.Events == "hosted") != want {
				t.Fatalf("loadEvents and EventsHosted disagree: %q, %v", cfg.Events, err)
			}
		})
	}
}
