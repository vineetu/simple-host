package config

import "testing"

func TestGeoIPDir(t *testing.T) {
	cases := []struct{ dataDir, env, want string }{
		{"/srv/simple-host/sites", "", "/srv/simple-host/geoip"}, // production layout
		{"/srv/simple-host/sites/", "", "/srv/simple-host/geoip"},
		{"./data/sites", "", "data/geoip"},
		{"/srv/simple-host/sites", "/var/lib/geo", "/var/lib/geo"},
	}
	for _, c := range cases {
		t.Setenv("DB_DSN", "postgres://test")
		t.Setenv("ADMIN_API_KEY", "test-key")
		t.Setenv("DATA_DIR", c.dataDir)
		t.Setenv("GEOIP_DIR", c.env)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.GeoIPDir != c.want {
			t.Errorf("DATA_DIR=%q GEOIP_DIR=%q: GeoIPDir = %q, want %q", c.dataDir, c.env, cfg.GeoIPDir, c.want)
		}
	}
}
