package main

import "testing"

func TestVersionString(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	cases := []struct {
		name     string
		injected string
		want     string
	}{
		{"release build", "v0.1.0", "v0.1.0"},
		{"unset falls back to dev", "", "dev"},
		{"whitespace falls back to dev", "   ", "dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			version = tc.injected
			if got := versionString(); got != tc.want {
				t.Fatalf("versionString() = %q, want %q", got, tc.want)
			}
		})
	}
}
