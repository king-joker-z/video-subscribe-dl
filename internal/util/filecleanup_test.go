package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveAssociatedFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "测试视频 [BV123]")

	// 创建视频和关联文件
	files := []string{
		base + ".mkv",
		base + ".nfo",
		base + "-thumb.jpg",
		base + ".danmaku.ass",
		base + ".danmaku.xml",
		base + ".zh-CN.srt",
		base + ".en.srt",
		base + ".zh-CN.ai.srt",
	}
	for _, f := range files {
		os.WriteFile(f, []byte("test"), 0644)
	}

	RemoveAssociatedFiles(base + ".mkv")

	// 视频文件不应被删除
	if _, err := os.Stat(base + ".mkv"); os.IsNotExist(err) {
		t.Error("video file should not be deleted")
	}

	// 关联文件应被删除
	for _, f := range files[1:] {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("associated file should be deleted: %s", f)
		}
	}
}

func TestRemoveEmptyDirs(t *testing.T) {
	boundary := t.TempDir()
	nested := filepath.Join(boundary, "a", "b", "c")
	os.MkdirAll(nested, 0755)

	RemoveEmptyDirs(nested, boundary, 3)

	// c, b, a 都应被删除
	if _, err := os.Stat(filepath.Join(boundary, "a")); !os.IsNotExist(err) {
		t.Error("empty dirs should be cleaned up")
	}

	// boundary 本身不应被删除
	if _, err := os.Stat(boundary); os.IsNotExist(err) {
		t.Error("boundary should not be deleted")
	}
}

func TestRemoveAssociatedFiles_AISubtitle(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "video")

	files := []string{
		base + ".mkv",
		base + ".zh-CN.ai.srt",
		base + ".ja.ai.ass",
	}
	for _, f := range files {
		os.WriteFile(f, []byte("test"), 0644)
	}

	RemoveAssociatedFiles(base + ".mkv")

	for _, f := range files[1:] {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("AI subtitle should be deleted: %s", f)
		}
	}
}

func TestRemoveEmptyDirs_NonEmpty(t *testing.T) {
	boundary := t.TempDir()
	nested := filepath.Join(boundary, "a", "b")
	os.MkdirAll(nested, 0755)
	// 在 a 中放一个文件
	os.WriteFile(filepath.Join(boundary, "a", "keep.txt"), []byte("keep"), 0644)

	RemoveEmptyDirs(nested, boundary, 3)

	// b 应被删除，a 不应（非空）
	if _, err := os.Stat(nested); !os.IsNotExist(err) {
		t.Error("empty dir b should be deleted")
	}
	if _, err := os.Stat(filepath.Join(boundary, "a")); os.IsNotExist(err) {
		t.Error("non-empty dir a should be kept")
	}
}
