package main

import "testing"

const sampleDiff = `diff --git a/src/cache/route_cache.go b/src/cache/route_cache.go
index 1111111..2222222 100644
--- a/src/cache/route_cache.go
+++ b/src/cache/route_cache.go
@@ -14,6 +14,20 @@ func NewRouteCache
 func NewRouteCache(r *redis.Client) *RouteCache {
-	return &RouteCache{r: r}
+	return &RouteCache{r: r, ttl: defaultTTL}
+	// TODO: metrics
 }
@@ -40,3 +54,4 @@
 tail context
-old line
+new line
+added line
diff --git a/newfile.txt b/newfile.txt
new file mode 100644
--- /dev/null
+++ b/newfile.txt
@@ -0,0 +1,2 @@
+hello
+world
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
--- a/gone.txt
+++ /dev/null
@@ -1,1 +0,0 @@
-bye
\ No newline at end of file
diff --git a/old_name.go b/new_name.go
similarity index 95%
rename from old_name.go
rename to new_name.go
diff --git a/image.png b/image.png
Binary files a/image.png and b/image.png differ
diff --git a/script.sh b/script.sh
old mode 100644
new mode 100755
diff --git "a/\343\203\206\343\202\271\343\203\210 file.go" "b/\343\203\206\343\202\271\343\203\210 file.go"
--- "a/\343\203\206\343\202\271\343\203\210 file.go"
+++ "b/\343\203\206\343\202\271\343\203\210 file.go"
@@ -1,2 +1,2 @@
 ctx
-削除された行
+追加された行
`

func TestParseUnifiedDiffFiles(t *testing.T) {
	files := ParseUnifiedDiff(sampleDiff)
	if len(files) != 7 {
		t.Fatalf("got %d files, want 7", len(files))
	}

	f := files[0]
	if f.Path() != "src/cache/route_cache.go" || len(f.Hunks) != 2 {
		t.Fatalf("file0: path=%q hunks=%d", f.Path(), len(f.Hunks))
	}
	if f.Hunks[0].Section != "func NewRouteCache" {
		t.Errorf("hunk section = %q", f.Hunks[0].Section)
	}
	if f.Added() != 4 || f.Deleted() != 2 {
		t.Errorf("file0 +%d -%d, want +4 -2", f.Added(), f.Deleted())
	}

	if !files[1].IsNew || files[1].Path() != "newfile.txt" || files[1].OldPath != "" {
		t.Errorf("file1 not detected as new: %+v", files[1])
	}
	if !files[2].IsDeleted || files[2].Path() != "gone.txt" {
		t.Errorf("file2 not detected as deleted: %+v", files[2])
	}
	if !files[3].IsRename || files[3].OldPath != "old_name.go" || files[3].NewPath != "new_name.go" || len(files[3].Hunks) != 0 {
		t.Errorf("file3 rename-only mismatch: %+v", files[3])
	}
	if !files[4].IsBinary {
		t.Errorf("file4 not detected as binary: %+v", files[4])
	}
	if files[5].Path() != "script.sh" || len(files[5].Hunks) != 0 {
		t.Errorf("file5 mode-change-only mismatch: %+v", files[5])
	}
	if files[6].Path() != "テスト file.go" {
		t.Errorf("file6 quoted path = %q", files[6].Path())
	}
}

func TestParseUnifiedDiffLineNumbers(t *testing.T) {
	files := ParseUnifiedDiff(sampleDiff)
	h := files[0].Hunks[0] // @@ -14,6 +14,20 @@
	want := []struct {
		kind         LineKind
		oldNo, newNo int
	}{
		{LineContext, 14, 14},
		{LineDel, 15, 0},
		{LineAdd, 0, 15},
		{LineAdd, 0, 16},
		{LineContext, 16, 17},
	}
	if len(h.Lines) != len(want) {
		t.Fatalf("hunk0 has %d lines, want %d", len(h.Lines), len(want))
	}
	for i, w := range want {
		l := h.Lines[i]
		if l.Kind != w.kind || l.OldNo != w.oldNo || l.NewNo != w.newNo {
			t.Errorf("line %d: kind=%v old=%d new=%d, want kind=%v old=%d new=%d",
				i, l.Kind, l.OldNo, l.NewNo, w.kind, w.oldNo, w.newNo)
		}
	}
}

func TestParseUnifiedDiffNoNewline(t *testing.T) {
	files := ParseUnifiedDiff(sampleDiff)
	lines := files[2].Hunks[0].Lines
	last := lines[len(lines)-1]
	if last.Kind != LineNoNewline {
		t.Errorf("last line kind = %v, want LineNoNewline", last.Kind)
	}
}

func TestParseUnifiedDiffEmpty(t *testing.T) {
	if files := ParseUnifiedDiff(""); len(files) != 0 {
		t.Errorf("empty diff parsed to %d files", len(files))
	}
}

func TestParseUnifiedDiffEmptyContextLine(t *testing.T) {
	// Some producers emit bare "" for empty context lines instead of " ".
	diff := "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,3 +1,3 @@\n x\n\n-y\n+z\n"
	files := ParseUnifiedDiff(diff)
	if len(files) != 1 || len(files[0].Hunks) != 1 {
		t.Fatalf("unexpected parse: %+v", files)
	}
	lines := files[0].Hunks[0].Lines
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	if lines[1].Kind != LineContext || lines[1].OldNo != 2 || lines[1].NewNo != 2 {
		t.Errorf("empty context line mishandled: %+v", lines[1])
	}
	if lines[2].Kind != LineDel || lines[2].OldNo != 3 {
		t.Errorf("del after empty context: %+v", lines[2])
	}
}

func TestParsePlainUnifiedDiffWithoutGitHeader(t *testing.T) {
	diff := "--- a/x.txt\n+++ b/x.txt\n@@ -1 +1 @@\n-a\n+b\n"
	files := ParseUnifiedDiff(diff)
	if len(files) != 1 || files[0].Path() != "x.txt" || len(files[0].Hunks) != 1 {
		t.Fatalf("plain diff mishandled: %+v", files)
	}
}
