package version

import "testing"

func TestGet(t *testing.T) {
	original := Version
	originalBuildType := BuildType
	originalDeployment := Deployment
	t.Cleanup(func() {
		Version = original
		BuildType = originalBuildType
		Deployment = originalDeployment
	})
	t.Setenv("CHATGPT2API_DEPLOYMENT", "")

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "normalizes whitespace", raw: " 1.2.3 \n", want: "1.2.3"},
		{name: "falls back when empty", raw: "", want: "0.0.0-dev"},
		{name: "falls back when whitespace", raw: " \t", want: "0.0.0-dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.raw
			if got := Get(); got != tt.want {
				t.Fatalf("Get() = %q, want %q", got, tt.want)
			}
		})
	}

	BuildType = " release "
	if got := GetBuildType(); got != "release" {
		t.Fatalf("GetBuildType() = %q, want release", got)
	}
	BuildType = ""
	if got := GetBuildType(); got != "source" {
		t.Fatalf("GetBuildType() = %q, want source", got)
	}

	Deployment = " docker "
	if got := GetDeployment(); got != "docker" {
		t.Fatalf("GetDeployment() = %q, want docker", got)
	}
	Deployment = ""
	if got := GetDeployment(); got != "binary" {
		t.Fatalf("GetDeployment() = %q, want binary", got)
	}
	t.Setenv("CHATGPT2API_DEPLOYMENT", "compose")
	if got := GetDeployment(); got != "compose" {
		t.Fatalf("GetDeployment() = %q, want env override", got)
	}
}
