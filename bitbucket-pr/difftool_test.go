package main

import (
	"strings"
	"testing"
)

const reorderSample = `diff --git a/first.go b/first.go
--- a/first.go
+++ b/first.go
@@ -1 +1 @@
-a
+b
diff --git a/second.go b/second.go
--- a/second.go
+++ b/second.go
@@ -1 +1 @@
-c
+d
diff --git a/old.go b/renamed.go
similarity index 90%
rename from old.go
rename to renamed.go
`

func TestReorderPatchMovesFocusFirst(t *testing.T) {
	out := reorderPatch(reorderSample, "second.go")
	if !strings.HasPrefix(out, "diff --git a/second.go b/second.go\n") {
		t.Errorf("second.go not moved to front:\n%s", out[:80])
	}
	// Nothing lost or duplicated.
	for _, path := range []string{"first.go", "second.go", "renamed.go"} {
		if n := strings.Count(out, "diff --git a/"+path); path != "renamed.go" && n != 1 {
			t.Errorf("section %s appears %d times", path, n)
		}
	}
	if len(out) != len(reorderSample) {
		t.Errorf("length changed: %d -> %d", len(reorderSample), len(out))
	}
}

func TestReorderPatchByOldPathOfRename(t *testing.T) {
	out := reorderPatch(reorderSample, "renamed.go")
	if !strings.HasPrefix(out, "diff --git a/old.go b/renamed.go\n") {
		t.Errorf("renamed.go section not moved to front:\n%s", out[:60])
	}
}

func TestReorderPatchNoOps(t *testing.T) {
	if out := reorderPatch(reorderSample, ""); out != reorderSample {
		t.Error("empty focus must be a no-op")
	}
	if out := reorderPatch(reorderSample, "missing.go"); out != reorderSample {
		t.Error("unknown path must be a no-op")
	}
	if out := reorderPatch(reorderSample, "first.go"); out != reorderSample {
		t.Error("already-first path must be a no-op")
	}
}

func TestDiffToolArgv(t *testing.T) {
	argv, useStdin := diffToolArgv([]string{"hunk", "patch", "--watch", "{patch}"}, "/tmp/x.patch")
	if useStdin {
		t.Error("placeholder present: useStdin must be false")
	}
	if argv[3] != "/tmp/x.patch" {
		t.Errorf("placeholder not replaced: %v", argv)
	}

	argv, useStdin = diffToolArgv([]string{"delta"}, "/tmp/x.patch")
	if !useStdin {
		t.Error("no placeholder: useStdin must be true")
	}
	if argv[0] != "delta" {
		t.Errorf("argv mutated: %v", argv)
	}
}

func TestDiffTabTitle(t *testing.T) {
	pr := &PullRequest{Title: "Fix everything"}
	if got := diffTabTitle("PR #{id}", 42, pr, "my-app"); got != "PR #42" {
		t.Errorf("got %q", got)
	}
	if got := diffTabTitle("{repo}#{id} {title}", 7, pr, "my-app"); got != "my-app#7 Fix everything" {
		t.Errorf("got %q", got)
	}
	if got := diffTabTitle("PR #{id}", 1, nil, "r"); got != "PR #1" {
		t.Errorf("nil pr: got %q", got)
	}
	long := &PullRequest{Title: strings.Repeat("あ", 40)}
	if got := diffTabTitle("{title}", 1, long, "r"); displayWidth(got) > 40 {
		t.Errorf("title not truncated to 40 cells: %q (%d)", got, displayWidth(got))
	}
}
