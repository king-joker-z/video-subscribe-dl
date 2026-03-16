package scheduler

import (
	"os"
	"path/filepath"
	"testing"
)

// ============================
// containsInvalidChars 测试
// ============================

func TestContainsInvalidChars_Normal(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"normal_name", false},
		{"中文目录名", false},
		{"video [BV123]", false},
		{"file-name_v2", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := containsInvalidChars(tt.name); got != tt.want {
			t.Errorf("containsInvalidChars(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestContainsInvalidChars_ControlChars(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"has\x00null", true},       // C0 control
		{"has\x1Fescape", true},     // C0 control
		{"has\x7Fdel", true},        // DEL
		{"has\u0080char", true},     // C1 control
		{"has\u009Fchar", true},     // C1 control
	}
	for _, tt := range tests {
		if got := containsInvalidChars(tt.name); got != tt.want {
			t.Errorf("containsInvalidChars(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestContainsInvalidChars_ZeroWidthChars(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"has\u200Bzwsp", true},     // zero-width space
		{"has\u200Frlm", true},      // RTL mark
		{"has\u2028lsep", true},     // line separator
		{"has\u2060wj", true},       // word joiner
		{"has\uFEFFbom", true},      // BOM
		{"has\uFFFDrepl", true},     // replacement char
	}
	for _, tt := range tests {
		if got := containsInvalidChars(tt.name); got != tt.want {
			t.Errorf("containsInvalidChars(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestContainsInvalidChars_VariationSelectors(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"has\uFE00vs", true},       // variation selector
		{"has\uFE0Fvs16", true},     // VS16
	}
	for _, tt := range tests {
		if got := containsInvalidChars(tt.name); got != tt.want {
			t.Errorf("containsInvalidChars(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// ============================
// removeEmptyDirs 测试
// ============================

func TestRemoveEmptyDirs_RemovesEmpty(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	os.MkdirAll(nested, 0755)

	removeEmptyDirs(nested)

	// c, b, a should all be removed (max 3 levels)
	if _, err := os.Stat(filepath.Join(root, "a")); !os.IsNotExist(err) {
		t.Error("expected dir 'a' to be removed")
	}
}

func TestRemoveEmptyDirs_StopsAtNonEmpty(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	os.MkdirAll(nested, 0755)

	// Put a file in "a" to prevent its removal
	os.WriteFile(filepath.Join(root, "a", "keep.txt"), []byte("keep"), 0644)

	removeEmptyDirs(nested)

	// c and b should be removed, but a should remain
	if _, err := os.Stat(filepath.Join(root, "a", "b")); !os.IsNotExist(err) {
		t.Error("expected dir 'b' to be removed")
	}
	if _, err := os.Stat(filepath.Join(root, "a")); os.IsNotExist(err) {
		t.Error("expected dir 'a' to remain (has file)")
	}
}

func TestRemoveEmptyDirs_MaxThreeLevels(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "1", "2", "3", "4", "5")
	os.MkdirAll(deep, 0755)

	removeEmptyDirs(deep)

	// Only 3 levels removed: 5, 4, 3. Dirs 1 and 2 should remain.
	if _, err := os.Stat(filepath.Join(root, "1", "2")); os.IsNotExist(err) {
		t.Error("expected dir '2' to remain (max 3 levels)")
	}
}

func TestRemoveEmptyDirs_NonexistentDir(t *testing.T) {
	// Should not panic
	removeEmptyDirs("/nonexistent/dir/path")
}
