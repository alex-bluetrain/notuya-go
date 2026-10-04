package notuyago_test

import (
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/alex-bluetrain/notuya-go/"

// allowed lists, per package, the in-module packages it may import. Layers
// only point down: application → presentation/session → transport. dp is
// pure codecs (no I/O), so the application layer combines it with session
// rather than session depending on it.
var allowed = map[string][]string{
	"pkg/transport/v35": {},
	"pkg/transport/udp": {"pkg/transport/v35"},
	"pkg/session":       {},
	"pkg/session/v35":   {"pkg/session", "pkg/transport/v35"},
	"pkg/dp":            {},
	"pkg/bulb":          {"pkg/dp", "pkg/session"},
	"pkg/discovery":     {"pkg/transport/udp"},
}

// forbidden std packages: the library never prints, logs, or touches the
// environment or filesystem.
var forbidden = []string{"os", "log", "log/slog"}

func TestLayering(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", "./pkg/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		pkg := strings.TrimPrefix(fields[0], module)
		seen[pkg] = true
		ok, known := allowed[pkg]
		if !known {
			t.Errorf("%s is not assigned to a layer; add it to allowed", pkg)
			continue
		}
		for _, imp := range fields[1:] {
			for _, f := range forbidden {
				if imp == f {
					t.Errorf("%s imports %s; the library must not do I/O of its own", pkg, imp)
				}
			}
			dep, internal := strings.CutPrefix(imp, module)
			if internal && !contains(ok, dep) {
				t.Errorf("%s imports %s, which breaks the layering", pkg, dep)
			}
		}
	}
	for pkg := range allowed {
		if !seen[pkg] {
			t.Errorf("allowed lists %s, which no longer exists", pkg)
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
