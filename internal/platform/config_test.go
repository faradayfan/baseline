package platform

import "testing"

func TestLoad(t *testing.T) {
	// minimal required env for a valid config
	base := map[string]string{
		"DATABASE_URL": "postgres://localhost/baseline",
		"MEMORY_SOURCE": "none",
	}

	tests := []struct {
		name    string
		env     map[string]string // overrides/additions on top of base
		wantErr bool
		check   func(t *testing.T, c Config)
	}{
		{
			name: "defaults applied",
			check: func(t *testing.T, c Config) {
				if c.Addr != ":8080" {
					t.Errorf("Addr = %q, want :8080", c.Addr)
				}
				if c.EmbedderDims != 768 {
					t.Errorf("EmbedderDims = %d, want 768", c.EmbedderDims)
				}
				if c.EmbedderModel != "nomic-embed-text" {
					t.Errorf("EmbedderModel = %q, want nomic-embed-text", c.EmbedderModel)
				}
			},
		},
		{
			name:    "missing database url fails closed",
			env:     map[string]string{"DATABASE_URL": ""},
			wantErr: true,
		},
		{
			name:    "unknown memory source fails closed",
			env:     map[string]string{"MEMORY_SOURCE": "redis"},
			wantErr: true,
		},
		{
			name:    "mem0 without url fails closed",
			env:     map[string]string{"MEMORY_SOURCE": "mem0"},
			wantErr: true,
		},
		{
			name: "mem0 with url ok",
			env:  map[string]string{"MEMORY_SOURCE": "mem0", "MEM0_URL": "http://mem0:8000"},
			check: func(t *testing.T, c Config) {
				if c.MemorySource != MemoryMem0 || c.Mem0URL != "http://mem0:8000" {
					t.Errorf("got source=%q url=%q", c.MemorySource, c.Mem0URL)
				}
			},
		},
		{
			name:    "non-positive embedder dims fails closed",
			env:     map[string]string{"EMBEDDER_DIMS": "0"},
			wantErr: true,
		},
		{
			name:    "non-numeric embedder dims fails closed",
			env:     map[string]string{"EMBEDDER_DIMS": "big"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv isolates and restores env per subtest.
			for k, v := range base {
				t.Setenv(k, v)
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			c, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got config %+v", c)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, c)
			}
		})
	}
}
